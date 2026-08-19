package zim

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"

	"encoding/binary"

	"github.com/edsrzf/mmap-go"
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
	contents mmap.MMap
	fh       *os.File
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

// Create a Zim FS server and maps the file to memory using mmap.
func NewZimFile(f *os.File) (*ZimFile, error) {
	header, err := ReadHeader(f)
	if err != nil {
		return nil, err
	}
	contents, err := mmap.Map(f, mmap.RDONLY, 0)
	if err != nil {
		return nil, err
	}

	return &ZimFile{
		Header:   header,
		contents: contents,
		fh:       f,
		clusters: make([]ClusterReader, header.ClusterCount),
		cache:    newClusterCache(MAX_CLUSTER_CACHE_SIZE),
	}, nil
}

// Close the zim file held in memory. The associated file handler is not closed.
func (zf *ZimFile) Close() error {
	return zf.contents.Unmap()
}

// Get the first entry whose path is less than or equal to path in the same
// namespace. This does not mean the index is exactly where the entry is but the lowest
// index where it can possibly be found. This is needed because paths in ZIM index are
// just files and don't contain the top directory entry. For example, there might be
// a path "example.com/assets/style.css" but no corresponding path for "example.com/assets".
// Therefore, callers of this function should verify if entry actually exists using
// GetDirEntry with the index
func (zf *ZimFile) getEntryLowerBound(start uint32, namespace rune, path string) (uint32, error) {
	low, high := start, zf.EntryCount
	var mid uint32
	var entry ZimEntry
	var err error
	target := Entry{
		Namespace: namespace,
		Path:      path,
	}
	for low < high {
		mid = (low + high) / 2
		entry, err = zf.getZimEntry(mid)
		if err != nil {
			return 0, err
		}
		if entry.Get().Compare(&target) < 0 {
			low = mid + 1
		} else {
			high = mid
		}
	}
	// If low equals zf.EntryCount in cases where the item's path is higher
	// than the last item, cap the return value to the last possible index
	if low != 0 && low == zf.EntryCount {
		low = zf.EntryCount - 1
	}

	return low, nil
}

// Get the pathname at offset.
func (zf *ZimFile) getPathName(start uint64) (string, error) {
	length := uint64(len(zf.contents))
	if start >= length {
		return "", CorruptData
	}
	// Pathnames are zero terminated.
	end := start
	for end < length && zf.contents[end] != 0 {
		end++
	}
	if end == length {
		return "", CorruptData
	}
	return string(zf.contents[start:end]), nil
}

// Get the directory entry located at index
func (zf *ZimFile) getZimEntry(index uint32) (ZimEntry, error) {
	if index >= zf.EntryCount {
		return nil, EntryDoesNotExist
	}

	pathListEnd, ok := ptrListEnd(zf.PathPtrPos, zf.EntryCount)
	if !ok || pathListEnd > uint64(len(zf.contents)) {
		return nil, CorruptData
	}
	pathList := zf.contents[zf.PathPtrPos:pathListEnd]

	offset := readUint64(pathList, uint64(index)*8)

	if !fits(offset, 2, uint64(len(zf.contents))) {
		return nil, CorruptData
	}
	mimeType := readUint16(zf.contents, offset)

	if IsRedirectEntry(mimeType) {
		// Redirect entry header: mimeType(2) + paramLen(1) + ns(1) +
		// revision(4) + redirectIndex(4) = 12 bytes.
		if !fits(offset, 12, uint64(len(zf.contents))) {
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
	if !fits(offset, 16, uint64(len(zf.contents))) {
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

func (zf *ZimFile) GetZimEntry(namespace rune, path string) (ZimEntry, error) {
	return zf.GetZimEntryFromStart(0, namespace, path)
}

func (zf *ZimFile) ResolveRedirect(entry *RedirectEntry) (ZimEntry, error) {
	return zf.getZimEntry(entry.RedirectIndex)
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
	dirent, err := zf.getZimEntry(lowerBound)
	if err != nil {
		return nil, err
	}
	entry := dirent.Get()
	if IsDeletedEntry(entry.MimeType) || IsLinkTargetEntry(entry.MimeType) {
		// Deprecated entries are hidden from lookup.
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
		return nil, CorruptData
	}

	if zf.clusters[entry.ClusterNumber] != nil {
		return zf.clusters[entry.ClusterNumber], nil
	}

	clusterListEnd, ok := ptrListEnd(zf.ClusterPtrPos, zf.ClusterCount)
	if !ok || clusterListEnd > uint64(len(zf.contents)) {
		return nil, CorruptData
	}
	clusterList := zf.contents[zf.ClusterPtrPos:clusterListEnd]

	start := readUint64(clusterList, uint64(entry.ClusterNumber)*8)
	if start >= uint64(len(zf.contents)) {
		return nil, CorruptData
	}

	cluster, err := NewCluster(zf.contents[start:])
	if err != nil {
		return nil, err
	}
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
		redirect, err := zf.getZimEntry(entry.RedirectIndex)
		if err != nil {
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

// Get the top-level children of the directory entry
func (zf *ZimFile) GetChildren(entry *DirectoryEntry, start uint32) []ZimEntry {
	// Given ZIM files do not actually store empty directories, we should iterate
	// the DirectoryEntry starting from the offset (which is where the first child
	// starts) till we find the first file whose prefix doesn't start with the
	// DirectoryEntry. So, offset 0 translates to the first child
	children := []ZimEntry{}
	var child ZimEntry
	var err error
	var parentPath string
	if entry.Path == "" { // for root nodes, we don't have any path
		parentPath = entry.Path
	} else {
		parentPath = entry.Path + "/"
	}
	// Map needed to ensure we do not add duplicate entries given we want only the
	// top-level children. For example, if we have:
	// example.com/index.html, example.com/js/index.js, example.com/js/neuron.js
	// we only want the children to be example.com/index.html and example.com/js
	seen := make(map[string]bool)
	for i := entry.Number + start; i < zf.EntryCount; i++ {
		child, err = zf.getZimEntry(i)
		// We will never get EntryDoesNotExist errors because index is capped
		// at EntryCount. But we can skip for the other errors.
		if err != nil {
			// Skip corrupt entries during listing; they will fail on access.
			continue
		}

		childEntry := child.Get()
		if IsDeletedEntry(childEntry.MimeType) || IsLinkTargetEntry(childEntry.MimeType) {
			// Deprecated entries are never listed.
			continue
		}
		if childEntry.Namespace != entry.Namespace {
			break
		}

		relativePath, isRelative := strings.CutPrefix(childEntry.Path, parentPath)
		if !isRelative {
			break
		}

		before, _, isDir := strings.Cut(relativePath, "/")
		if _, ok := seen[before]; ok {
			// nested files which we don't care about
			continue
		}
		seen[before] = true
		if isDir {
			children = append(children, NewDirectoryEntry(childEntry.Namespace, parentPath+before, i))
		} else {
			children = append(children, child)
		}

	}
	return children
}
