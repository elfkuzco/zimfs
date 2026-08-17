package zim

import "strings"

const (
	DIRECTORY_ENTRY_MIMETYPE uint16 = 0xfffc
	DELETED_ENTRY_MIMETYPE   uint16 = 0xfffd
	LINK_TARGET_MIMETYPE     uint16 = 0xfffe
	REDIRECT_ENTRY_MIMETYPE  uint16 = 0xffff
)

type ZimEntry interface {
	Get() *Entry
}

type Entry struct {
	// MIME type number as defined in the MIME type list
	MimeType     uint16
	ParameterLen uint8
	// Which namespace this directory entry belongs
	Namespace rune
	Revision  uint32
	// String with the path as referred in the path pointer list
	Path string
}

func IsRedirectEntry(mimeType uint16) bool {
	return mimeType == REDIRECT_ENTRY_MIMETYPE
}

func IsDeletedEntry(mimeType uint16) bool {
	return mimeType == DELETED_ENTRY_MIMETYPE
}

func IsDirectoryEntry(mimeType uint16) bool {
	return mimeType == DIRECTORY_ENTRY_MIMETYPE
}

func IsLinkTargetEntry(mimeType uint16) bool {
	return mimeType == LINK_TARGET_MIMETYPE
}

func (e *Entry) IsRedirectEntry() bool {
	return IsRedirectEntry(e.MimeType)
}

func (e *Entry) IsDirectoryEntry() bool {
	return IsDirectoryEntry(e.MimeType)
}

func (e *Entry) Equal(other *Entry) bool {
	return e.Namespace == other.Namespace && e.Path == other.Path
}

func (e *Entry) Compare(other *Entry) int {
	if e.Namespace < other.Namespace {
		return -1
	}
	if e.Namespace > other.Namespace {
		return 1
	}
	return strings.Compare(e.Path, other.Path)
}

func (e *Entry) Get() *Entry {
	return e
}

type ContentEntry struct {
	Entry
	// Cluster number in which the data of this directory entry is stored
	ClusterNumber uint32
	// Blob number inside the compressed cluster where the contents are stored
	BlobNumber uint32
}

type RedirectEntry struct {
	Entry
	// Pointer to the directory entry of the redirect target
	RedirectIndex uint32
}

// ZIM files do not actually have a directory entry. This container merely indicates
// the path has children in it's hierachy
type DirectoryEntry struct {
	Entry
	Offset uint32 // where the first chlid path starts
}

func NewDirectoryEntry(namespace rune, path string, offset uint32) *DirectoryEntry {
	return &DirectoryEntry{
		Entry{
			MimeType:     DIRECTORY_ENTRY_MIMETYPE,
			ParameterLen: 0,
			Namespace:    namespace,
			Revision:     0,
			Path:         path,
		},
		offset,
	}
}
