package zim

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"encoding/binary"
)

// fits reports whether the range [offset, offset+n) lies within a buffer of
// the given length, without risking integer overflow in the offset+n sum.
func fits(offset, n, length uint64) bool {
	return offset <= length && n <= length-offset
}

// readUint16/32/64 read a little-endian integer without bounds checking.
// Callers must have already validated the range (for example, with fits).
func readUint16(data []byte, offset uint64) uint16 {
	return binary.LittleEndian.Uint16(data[offset : offset+2])
}

func readUint32(data []byte, offset uint64) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}

func readUint64(data []byte, offset uint64) uint64 {
	return binary.LittleEndian.Uint64(data[offset : offset+8])
}

type ZimFile struct {
	*Header
	contents []byte
	// length of the contents slice
	length uint64
	// mutex for restricting access to clusters
	mu       sync.Mutex
	clusters []ClusterReader

	// shared bounded cache for decompressed cluster data.
	cache *clusterCache
}

var (
	EntryDoesNotExist = errors.New("entry does not exist")
	InvalidEntry      = errors.New("cannot perform operation on entry")
	CorruptData       = errors.New("corrupt or truncated zim data")
)

// ptrListEnd returns start + count*8, and reports whether the addition
// overflows uint64. It is used to validate on-disk pointer lists before slicing.
func ptrListEnd(start uint64, count uint32) (uint64, bool) {
	size := uint64(count) * 8
	if start > ^uint64(0)-size {
		return 0, false
	}
	return start + size, true
}

// NewZimFile creates a ZimFile backed by the given bytes. The caller is
// responsible for the lifetime of data
func NewZimFile(data []byte) (*ZimFile, error) {
	header, err := parseHeader(data)
	if err != nil {
		return nil, err
	}

	return &ZimFile{
		Header:   header,
		contents: data,
		length:   uint64(len(data)),
		clusters: make([]ClusterReader, header.ClusterCount),
		cache:    newClusterCache(MAX_CLUSTER_CACHE_SIZE),
	}, nil
}

// Return the first index i >= start such that the entry at i sorts
// at or after (namespace, path)
func (zf *ZimFile) lowerBound(start uint32, namespace rune, path string) (uint32, error) {
	low, high := start, zf.EntryCount
	target := Entry{
		Namespace: namespace,
		Path:      path,
	}
	for low < high {
		mid := (low + high) / 2
		entry, err := zf.GetZimEntryAtIndex(mid)
		if err != nil {
			return 0, err
		}
		if entry.Get().Compare(&target) < 0 {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low, nil
}

// Find the entry at or before the given index in the same namespace.
// The returned index is a lower bound as the entry may not exist exactly there.
// ZIM indices only contain files, not directory entries, so callers must verify
// the result.
func (zf *ZimFile) getEntryLowerBound(start uint32, namespace rune, path string) (uint32, error) {
	low, err := zf.lowerBound(start, namespace, path)
	if err != nil {
		return 0, err
	}
	// If low equals zf.EntryCount in cases where the item's path is higher
	// than the last item, cap the return value to the last possible index
	if low != 0 && low == zf.EntryCount {
		low = zf.EntryCount - 1
	}
	return low, nil
}

// FirstIndexInNamespace returns the first directory entry index in namespace,
// or EntryCount if the namespace has no entries.
func (zf *ZimFile) FirstIndexInNamespace(namespace rune) (uint32, error) {
	return zf.lowerBound(0, namespace, "")
}

// getPathBytes returns the zero-terminated path at offset as a sub-slice of the
// mmap, without copying.
func (zf *ZimFile) getPathBytes(start uint64) ([]byte, error) {
	if start >= zf.length {
		slog.Error("entry does not have space for reading path data")
		return nil, CorruptData
	}
	end := start
	for end < zf.length && zf.contents[end] != 0 {
		end++
	}
	if end == zf.length {
		slog.Error("entry does not have space for reading path data")
		return nil, CorruptData
	}
	return zf.contents[start:end], nil
}

// Get the pathname at offset.
func (zf *ZimFile) getPathName(start uint64) (string, error) {
	b, err := zf.getPathBytes(start)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Get the directory entry located at index
func (zf *ZimFile) GetZimEntryAtIndex(index uint32) (ZimEntry, error) {
	if index >= zf.EntryCount {
		slog.Error("index is greater than entry count", "index", index, "entryCount", zf.EntryCount)
		return nil, EntryDoesNotExist
	}

	pathListEnd, ok := ptrListEnd(zf.PathPtrPos, zf.EntryCount)
	if !ok || pathListEnd > zf.length {
		slog.Error("pathList end index exceeds length of file", "end", pathListEnd, "length", zf.length)
		return nil, CorruptData
	}
	pathList := zf.contents[zf.PathPtrPos:pathListEnd]

	offset := readUint64(pathList, uint64(index)*8)

	if !fits(offset, 2, zf.length) {
		slog.Error("entry does not have space for reading mimetype information")
		return nil, CorruptData
	}
	mimeType := readUint16(zf.contents, offset)

	if IsRedirectEntry(mimeType) {
		// Redirect entry header: mimeType(2) + paramLen(1) + ns(1) +
		// revision(4) + redirectIndex(4) = 12 bytes.
		if !fits(offset, 12, zf.length) {
			slog.Error("entry does not have space for reading contents")
			return nil, CorruptData
		}
		path, err := zf.getPathName(offset + 12)
		if err != nil {
			return nil, err
		}
		return NewRedirectEntry(
			zf.contents[offset+2],
			rune(zf.contents[offset+3]),
			readUint32(zf.contents, offset+4),
			path,
			index,
			readUint32(zf.contents, offset+8),
		), nil
	}

	// Content, deleted, and link-target entries share a 16-byte header with the
	// url at offset 16. Only content entries carry cluster/blob fields.
	if !fits(offset, 16, zf.length) {
		slog.Error("entry does not have space for reading contents")
		return nil, CorruptData
	}
	path, err := zf.getPathName(offset + 16)
	if err != nil {
		return nil, err
	}

	switch {
	case IsDeletedEntry(mimeType):
		return NewDeletedEntry(
			zf.contents[offset+2],
			rune(zf.contents[offset+3]),
			readUint32(zf.contents, offset+4),
			path,
			index,
		), nil
	case IsLinkTargetEntry(mimeType):
		return NewLinkTargetEntry(
			zf.contents[offset+2],
			rune(zf.contents[offset+3]),
			readUint32(zf.contents, offset+4),
			path,
			index,
		), nil
	default:
		return NewContentEntry(
			mimeType,
			zf.contents[offset+2],
			rune(zf.contents[offset+3]),
			readUint32(zf.contents, offset+4),
			path,
			index,
			readUint32(zf.contents, offset+8),
			readUint32(zf.contents, offset+12),
		), nil
	}
}

// Returns the mime type, namespace, and path (as a sub-slice of
// the mmap) of the entry at index
func (zf *ZimFile) readDirentInfo(index uint32) (uint16, rune, []byte, error) {
	if index >= zf.EntryCount {
		return 0, 0, nil, EntryDoesNotExist
	}

	pathListEnd, ok := ptrListEnd(zf.PathPtrPos, zf.EntryCount)
	if !ok || pathListEnd > zf.length {
		return 0, 0, nil, CorruptData
	}
	pathList := zf.contents[zf.PathPtrPos:pathListEnd]

	offset := readUint64(pathList, uint64(index)*8)
	if !fits(offset, 4, zf.length) {
		return 0, 0, nil, CorruptData
	}
	mimeType := readUint16(zf.contents, offset)
	namespace := rune(zf.contents[offset+3])

	pathStart := offset + 16
	if IsRedirectEntry(mimeType) {
		pathStart = offset + 12
	}
	path, err := zf.getPathBytes(pathStart)
	if err != nil {
		return 0, 0, nil, err
	}
	return mimeType, namespace, path, nil
}

func (zf *ZimFile) GetZimEntry(namespace rune, path string) (ZimEntry, error) {
	return zf.GetZimEntryFromStart(0, namespace, path)
}

func (zf *ZimFile) ResolveRedirect(entry *RedirectEntry) (ZimEntry, error) {
	return zf.GetZimEntryAtIndex(entry.RedirectIndex)
}

func (zf *ZimFile) GetZimEntryFromStart(start uint32, namespace rune, path string) (ZimEntry, error) {
	target := Entry{
		Namespace: namespace,
		Path:      path,
	}
	lowerBound, err := zf.getEntryLowerBound(start, namespace, path)
	if err != nil {
		return nil, err
	}
	dirent, err := zf.GetZimEntryAtIndex(lowerBound)
	if err != nil {
		return nil, err
	}
	entry := dirent.Get()
	if IsDeletedEntry(entry.MimeType) || IsLinkTargetEntry(entry.MimeType) {
		// Deprecated entries are hidden from lookup.
		slog.Warn("skipping deleted/link target entries in zim")
		return nil, EntryDoesNotExist
	}
	if entry.Equal(&target) {
		return dirent, nil
	} else {
		// If entry starts with target prefix, then, target must be a directory.
		if strings.HasPrefix(entry.Path, target.Path+"/") {
			return NewDirectoryEntry(namespace, path, lowerBound), nil
		}
	}
	return nil, EntryDoesNotExist
}

// Get or create cluster for entry
func (zf *ZimFile) GetOrCreateCluster(entry *ContentEntry) (ClusterReader, error) {
	zf.mu.Lock()
	defer zf.mu.Unlock()

	if entry.ClusterNumber >= zf.ClusterCount {
		slog.Error("cluster number is greater than total number of clusters",
			"clusterNumber", entry.ClusterNumber, "clusterCount", zf.ClusterCount,
		)
		return nil, CorruptData
	}

	if zf.clusters[entry.ClusterNumber] != nil {
		return zf.clusters[entry.ClusterNumber], nil
	}

	clusterListEnd, ok := ptrListEnd(zf.ClusterPtrPos, zf.ClusterCount)
	if !ok || clusterListEnd > zf.length {
		slog.Error("clusterList end index exceeds length of file", "end",
			clusterListEnd, "length", zf.length,
		)
		return nil, CorruptData
	}
	clusterList := zf.contents[zf.ClusterPtrPos:clusterListEnd]

	start := readUint64(clusterList, uint64(entry.ClusterNumber)*8)
	if start >= zf.length {
		return nil, CorruptData
	}

	slog.Debug("creating new cluster", "clusterNumber", entry.ClusterNumber,
		"start", start,
	)

	cluster, err := NewCluster(zf.contents[start:])
	if err != nil {
		return nil, err
	}
	// we don't need to cache UncompressedCluster entries since they are on
	// disk and we can obtain a slice directly on it.
	if cc, ok := cluster.(*CompressedCluster); ok {
		cc.clusterNumber = entry.ClusterNumber
		cc.cache = zf.cache
	}
	zf.clusters[entry.ClusterNumber] = cluster
	return cluster, nil
}

// Read the contents of a content entry
func (zf *ZimFile) Read(entry ZimEntry, offset int64, dst []byte) (int, error) {
	var cluster ClusterReader
	var contentEntry *ContentEntry
	var err error
	switch entry := entry.(type) {
	case *ContentEntry:
		contentEntry = entry
		goto begin
	case *RedirectEntry:
		redirect, err := zf.GetZimEntryAtIndex(entry.RedirectIndex)
		if err != nil {
			slog.Error("could not find entry from redirect index",
				"redirectIndex", entry.RedirectIndex,
			)
			return 0, err
		}
		if e, ok := redirect.(*ContentEntry); !ok {
			return 0, InvalidEntry
		} else {
			contentEntry = e
			goto begin
		}
	default:
		return 0, InvalidEntry
	}

begin:
	cluster, err = zf.GetOrCreateCluster(contentEntry)
	slog.Debug("reading contents from cluster", "compression", cluster.GetCompression())
	if err != nil {
		return 0, err
	}
	contents, err := cluster.GetBlob(contentEntry.BlobNumber)
	if err != nil {
		return 0, err
	}
	reader := bytes.NewReader(contents)
	return reader.ReadAt(dst, offset)
}

// Returns the next top-level child of dir, scanning from the absolute
// entry index startIndex. It returns the child, the absolute index to resume
// from (nextIndex), and found=false when the directory is exhausted.
func (zf *ZimFile) NextChild(dir *DirectoryEntry, startIndex uint32) (ZimEntry, uint32, bool, error) {
	var parentPath []byte
	if dir.Path != "" {
		parentPath = []byte(dir.Path + "/")
	}

	for i := startIndex; i < zf.EntryCount; i++ {
		mimeType, namespace, path, err := zf.readDirentInfo(i)
		if err != nil {
			if errors.Is(err, EntryDoesNotExist) {
				return nil, 0, false, nil
			}
			// Skip corrupt entries during listing.
			continue
		}

		if namespace != dir.Namespace {
			return nil, 0, false, nil
		}
		if !bytes.HasPrefix(path, parentPath) {
			return nil, 0, false, nil
		}

		rel := path[len(parentPath):]
		slash := bytes.IndexByte(rel, '/')
		var name []byte
		isDir := false
		if slash < 0 {
			name = rel
		} else {
			name = rel[:slash]
			isDir = true
		}
		if len(name) == 0 {
			continue
		}

		if IsDeletedEntry(mimeType) || IsLinkTargetEntry(mimeType) {
			// Deprecated entries are never listed.
			continue
		}

		// Find the end of this child's span: the next sibling index.
		// ZIM entries are listed in lexicographical order. So, before we
		// find the next sibling, we are going to have to exhaust the children
		// or descendants of this entry.
		next := i + 1
		for next < zf.EntryCount {
			_, namespace2, path2, err2 := zf.readDirentInfo(next)
			if err2 != nil {
				break
			}
			if namespace2 != dir.Namespace {
				break
			}
			if !bytes.HasPrefix(path2, parentPath) {
				break
			}
			rel2 := path2[len(parentPath):]
			slash2 := bytes.IndexByte(rel2, '/')
			var name2 []byte
			if slash2 < 0 {
				name2 = rel2
			} else {
				name2 = rel2[:slash2]
			}
			if !bytes.Equal(name2, name) {
				break
			}
			next++
		}

		var child ZimEntry
		if isDir {
			child = NewDirectoryEntry(namespace, string(parentPath)+string(name), i)
		} else {
			child, err = zf.GetZimEntryAtIndex(i)
			if err != nil {
				continue
			}
		}

		return child, next, true, nil
	}
	return nil, 0, false, nil
}

// GetChildren returns all top-level children of the directory entry, starting
// after start entries. It is a convenience wrapper around NextChild for
// non-streaming callers and tests.
func (zf *ZimFile) GetChildren(entry *DirectoryEntry, start uint32) []ZimEntry {
	children := []ZimEntry{}
	index := entry.Number + start
	for {
		child, next, found, err := zf.NextChild(entry, index)
		if err != nil || !found {
			return children
		}
		children = append(children, child)
		index = next
	}
}
