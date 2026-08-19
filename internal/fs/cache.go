package fs

import (
	"sync"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse/fuseops"
)

type InodeCache struct {
	mu sync.Mutex

	// The collection of live inodes, keyed by inode ID. No ID less than
	// fuseops.RootInodeID is ever used.
	inodes map[fuseops.InodeID]*inode

	// The mapping of namespace+paths in zim to inode ID.
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
	key := string(entry.Namespace) + "/" + entry.Path

	c.mu.Lock()
	defer c.mu.Unlock()
	c.inodes[inode.id] = inode
	c.paths[key] = inode.id
}

func (c *InodeCache) getInodeById(id fuseops.InodeID) (*inode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inode, ok := c.inodes[id]
	return inode, ok
}

func (c *InodeCache) getInodeIdByNsPath(namespace rune, path string) (fuseops.InodeID, bool) {
	key := string(namespace) + "/" + path

	c.mu.Lock()
	defer c.mu.Unlock()
	inodeId, ok := c.paths[key]
	return inodeId, ok
}

// getOrAddInode returns the live inode for namespace+path, or inserts and
// returns a newly built one. The attributes are precomputed by the caller so the
// cache lock is never held during (potentially slow) attribute computation.
func (c *InodeCache) getOrAddInode(namespace rune, path string, attrs fuseops.InodeAttributes, entry zim.ZimEntry, allocate func() fuseops.InodeID) *inode {
	key := string(namespace) + "/" + path

	c.mu.Lock()
	defer c.mu.Unlock()

	if id, ok := c.paths[key]; ok {
		if inode, ok := c.inodes[id]; ok {
			return inode
		}
		// The path mapping survived but the inode was evicted; reuse its ID.
		inode := newInode(id, attrs, entry)
		c.inodes[id] = inode
		return inode
	}

	id := allocate()
	inode := newInode(id, attrs, entry)
	c.inodes[id] = inode
	c.paths[key] = id
	return inode
}

func (c *InodeCache) incrementLookup(id fuseops.InodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if inode, ok := c.inodes[id]; ok {
		inode.lookedUp = true
		inode.lookupCount++
	}
}

func (c *InodeCache) forgetInode(id fuseops.InodeID, n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	inode, ok := c.inodes[id]
	if !ok {
		return
	}
	if n > inode.lookupCount {
		n = inode.lookupCount
	}
	inode.lookupCount -= n

	if inode.lookedUp && inode.lookupCount == 0 {
		c.removeLocked(id, inode)
	}
}

func (c *InodeCache) removeLocked(id fuseops.InodeID, inode *inode) {
	entry := inode.entry.Get()
	key := string(entry.Namespace) + "/" + entry.Path
	delete(c.inodes, id)
	delete(c.paths, key)
}
