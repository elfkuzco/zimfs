package zim

import (
	"errors"
	"os"
	"strings"

	"encoding/binary"

	"github.com/edsrzf/mmap-go"
)

type ZimFile struct {
	*Header
	contents mmap.MMap
	fh       *os.File
}

var (
	EntryDoesNotExist = errors.New("entry does not exist")
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
	}, nil
}

// Close the zim file held in memory and it's associated file handler
func (zf *ZimFile) Close() error {
	defer zf.fh.Close()
	return zf.contents.Unmap()
}

func (zf *ZimFile) readUint16(offset uint64) uint16 {
	return binary.LittleEndian.Uint16(zf.contents[offset : offset+2])
}

func (zf *ZimFile) readUint32(offset uint64) uint32 {
	return binary.LittleEndian.Uint32(zf.contents[offset : offset+4])
}

func (zf *ZimFile) readUint64(offset uint64) uint64 {
	return binary.LittleEndian.Uint64(zf.contents[offset : offset+8])
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
	offset := binary.LittleEndian.Uint64(pathList[index*8:])
	mimeType := zf.readUint16(offset)

	if IsDeletedEntry(mimeType) || IsLinkTargetEntry(mimeType) {
		return nil, EntryDoesNotExist
	}

	paramLen := zf.contents[offset+2]
	ns := zf.contents[offset+3]
	revision := zf.readUint32(offset + 4)
	if IsRedirectEntry(mimeType) {
		return &RedirectEntry{
			Entry{
				mimeType,
				paramLen,
				rune(ns),
				revision,
				zf.getPathName(offset + 12),
			},
			zf.readUint32(offset + 8),
		}, nil
	}
	return &ContentEntry{
		Entry{
			mimeType,
			paramLen,
			rune(ns),
			revision,
			zf.getPathName(offset + 16),
		},
		zf.readUint32(offset + 8),
		zf.readUint32(offset + 12),
	}, nil
}

func (zf *ZimFile) GetZimEntry(namespace rune, path string) (ZimEntry, error) {
	return zf.GetZimEntryFromStart(0, namespace, path)
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
