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

func ReadUint16(data []byte, offset uint64) uint16 {
	return binary.LittleEndian.Uint16(data[offset : offset+2])
}

func ReadUint32(data []byte, offset uint64) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}

func ReadUint64(data []byte, offset uint64) uint64 {
	return binary.LittleEndian.Uint64(data[offset : offset+8])
}

type ZimFile struct {
	*Header
	contents mmap.MMap
	fh       *os.File
	// mutex for restricting access to clusters
	mu       sync.Mutex
	clusters []ClusterReader
}

var (
	EntryDoesNotExist = errors.New("entry does not exist")
	EntryDeprecated   = errors.New("entry is deprecated")
	InvalidEntry      = errors.New("cannot perform operation on entry")
)

// Create a Zim FS server and maps the file to memory using mmap.
func NewZimFile(f *os.File) (*ZimFile, error) {
	var err error
	header, err := ReadHeader(f)
	if err != nil {
		return nil, err
	}
	contents, err := mmap.Map(f, mmap.RDONLY, 0)
	if err != nil {
		return nil, err
	}

	return &ZimFile{
		header,
		contents,
		f,
		sync.Mutex{},
		make([]ClusterReader, header.ClusterCount),
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
func (zf *ZimFile) getEntryLowerBound(start uint32, namespace rune, path string) uint32 {
	var low uint32
	low, high := start, zf.EntryCount
	var mid uint32
	var err error
	var entry ZimEntry
	target := Entry{
		Namespace: namespace,
		Path:      path,
	}
	for low < high {
		mid = (low + high) / 2
		entry, err = zf.getZimEntry(mid)
		if err != nil { // DeletedEntry or  LinkTargetEntry?
			low = mid + 1
			continue
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

	return low
}

// Get the pathname at offset.
func (zf *ZimFile) getPathName(start uint64) string {
	// Pathnames are zero terminated.
	end := start
	length := uint64(len(zf.contents))
	for end < length && zf.contents[end] != 0 {
		end++
	}
	return string(zf.contents[start:end])
}

// Get the directory entry located at index
func (zf *ZimFile) getZimEntry(index uint32) (ZimEntry, error) {
	if index >= zf.EntryCount {
		return nil, EntryDoesNotExist
	}
	pathList := zf.contents[zf.PathPtrPos : zf.PathPtrPos+uint64(zf.EntryCount)*8]
	offset := ReadUint64(pathList, uint64(index*8))
	mimeType := ReadUint16(zf.contents, offset)

	if IsDeletedEntry(mimeType) || IsLinkTargetEntry(mimeType) {
		return nil, EntryDeprecated
	}

	paramLen := zf.contents[offset+2]
	ns := zf.contents[offset+3]
	revision := ReadUint32(zf.contents, offset+4)
	if IsRedirectEntry(mimeType) {
		return NewRedirectEntry(
			paramLen,
			rune(ns),
			revision,
			zf.getPathName(offset+12),
			index,
			ReadUint32(zf.contents, offset+8),
		), nil
	}
	return NewContentEntry(
		mimeType,
		paramLen,
		rune(ns),
		revision,
		zf.getPathName(offset+16),
		index,
		ReadUint32(zf.contents, offset+8),
		ReadUint32(zf.contents, offset+12),
	), nil
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
	lowerBound := zf.getEntryLowerBound(start, namespace, path)
	dirent, err := zf.getZimEntry(lowerBound)
	if err != nil {
		return nil, err
	}
	entry := dirent.Get()
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
	if zf.clusters[entry.ClusterNumber] == nil {
		clusterList := zf.contents[zf.ClusterPtrPos : zf.ClusterPtrPos+uint64(zf.ClusterCount)*8]
		start := ReadUint64(clusterList, uint64(entry.ClusterNumber*8))
		cluster, err := NewCluster(zf.contents[start:])
		if err != nil {
			return nil, err
		}
		zf.clusters[entry.ClusterNumber] = cluster
	}
	return zf.clusters[entry.ClusterNumber], nil
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
	parentPath := entry.Path + "/"
	// Map needed to ensure we do not add duplicate entries given we want only the
	// top-level children. For example, if we have:
	// example.com/index.html, example.com/js/index.js, example.com/js/neuron.js
	// we only want the children to be example.com/index.html and example.com/js
	seen := make(map[string]bool)
	for i := entry.Number + start; i < zf.EntryCount; i++ {
		child, err = zf.getZimEntry(i)
		// We will never get EntryDoesNotExist errors because index is capped
		// at EntryCount. But we can skip for the other errors. For now, the only
		// other error is EntryDeprecated which we don't care about
		if err != nil {
			continue
		}

		childEntry := child.Get()
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
