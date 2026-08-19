package fs

import (
	"time"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse/fuseops"
)

type inode struct {
	id         fuseops.InodeID
	attributes fuseops.InodeAttributes
	entry      zim.ZimEntry

	// Guarded by InodeCache.mu.
	// lookupCount tracks the kernel's references to this inode; lookedUp is
	// true once it has been handed out via LookUpInode.
	lookupCount uint64
	lookedUp    bool
}

// Create a new inode with the supplied attributes, which need not contain
// time-related information (the inode object will take care of that).
func newInode(id fuseops.InodeID, attrs fuseops.InodeAttributes, entry zim.ZimEntry) *inode {
	// Update time info.
	now := time.Now()
	attrs.Mtime = now
	attrs.Crtime = now

	inode := &inode{
		id:         id,
		entry:      entry,
		attributes: attrs,
	}

	return inode
}

// build pathname of child with respect to the inode
func (in *inode) buildPathName(name string) string {
	var path string
	if in.id == fuseops.RootInodeID {
		return name
	} else {
		path = in.entry.Get().Path + "/" + name
	}
	return path
}
