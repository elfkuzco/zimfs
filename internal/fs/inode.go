package fs

import (
	"container/list"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse/fuseops"
)

type inode struct {
	id    fuseops.InodeID
	entry zim.ZimEntry

	// Guarded by InodeCache.mu.
	attributes   fuseops.InodeAttributes
	materialized bool
	lookupCount  uint64
	lookedUp     bool
	lruElement   *list.Element
	parent       fuseops.InodeID
}

// newInode creates an unmaterialized inode. Attributes are computed lazily on
// the first GetInodeAttributes/LookUpInode.
func newInode(id fuseops.InodeID, entry zim.ZimEntry) *inode {
	return &inode{
		id:    id,
		entry: entry,
	}
}

// build pathname of child with respect to the inode
func (in *inode) buildPathName(name string) string {
	if in.id == fuseops.RootInodeID {
		return name
	}
	return in.entry.Get().Path + "/" + name
}
