package fs

import (
	"github.com/jacobsa/fuse/fuseops"
)

type InodeCache struct {
	// The collection of live inodes, keyed by inode ID. No ID less than
	// fuseops.RootInodeID is ever used.
	inodes map[fuseops.InodeID]*inode

	// The mappng of namespace+paths in zim to inode ID
	paths map[string]fuseops.InodeID
}

func NewCache() *InodeCache {
	return &InodeCache{
		inodes: make(map[fuseops.InodeID]*inode),
		paths:  make(map[string]fuseops.InodeID),
	}
}

// Add inode to cache
func (c *InodeCache) addInode(inode *inode) {
	entry := inode.entry.Get()
	c.inodes[inode.id] = inode
	c.paths[string(entry.Namespace)+"/"+entry.Path] = inode.id
}

func (c *InodeCache) getInodeById(id fuseops.InodeID) (*inode, bool) {
	inode, ok := c.inodes[id]
	return inode, ok
}

func (c *InodeCache) getInodeIdByNsPath(namespace rune, path string) (fuseops.InodeID, bool) {
	key := string(namespace) + "/" + path
	inodeId, ok := c.paths[key]
	return inodeId, ok
}
