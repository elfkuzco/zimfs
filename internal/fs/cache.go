package fs

import (
	"container/list"
	"sync"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse/fuseops"
)

const defaultMaxInodes = 1_000

type pathKey struct {
	namespace rune
	path      string
}

type InodeCache struct {
	mu sync.Mutex

	inodes map[fuseops.InodeID]*inode
	paths  map[pathKey]fuseops.InodeID

	lru       *list.List
	maxInodes int
}

func NewCache() *InodeCache {
	return &InodeCache{
		inodes:    make(map[fuseops.InodeID]*inode),
		paths:     make(map[pathKey]fuseops.InodeID),
		lru:       list.New(),
		maxInodes: defaultMaxInodes,
	}
}

// addInode inserts a pre-built inode (used for the root inode).
func (c *InodeCache) addInode(inode *inode) {
	entry := inode.entry.Get()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inodes[inode.id] = inode
	c.paths[pathKey{entry.Namespace, entry.Path}] = inode.id
	inode.lruElement = c.lru.PushFront(inode)
}

func (c *InodeCache) getInodeById(id fuseops.InodeID) (*inode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inode, ok := c.inodes[id]
	if ok {
		c.lru.MoveToFront(inode.lruElement)
	}
	return inode, ok
}

// getInodeByNsPath returns the cached inode for namespace+path, incrementing its
// lookup count if lookup is true.
func (c *InodeCache) getInodeByNsPath(namespace rune, path string, lookup bool) (*inode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id, ok := c.paths[pathKey{namespace, path}]
	if !ok {
		return nil, false
	}
	inode, ok := c.inodes[id]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(inode.lruElement)
	if lookup {
		inode.lookedUp = true
		inode.lookupCount++
	}
	return inode, true
}

// getOrAddInode returns the inode for namespace+path, inserting a newly built one
// if needed. When lookup is true the returned inode's lookup count is incremented
// atomically with the insertion.
func (c *InodeCache) getOrAddInode(namespace rune, path string, entry zim.ZimEntry, allocate func() fuseops.InodeID, lookup bool) *inode {
	key := pathKey{namespace, path}

	c.mu.Lock()
	defer c.mu.Unlock()

	if id, ok := c.paths[key]; ok {
		if inode, ok := c.inodes[id]; ok {
			c.lru.MoveToFront(inode.lruElement)
			if lookup {
				inode.lookedUp = true
				inode.lookupCount++
			}
			return inode
		}
		// Stale mapping (should not happen since both maps are updated together).
		delete(c.paths, key)
	}

	id := allocate()
	inode := newInode(id, entry)
	if lookup {
		inode.lookedUp = true
		inode.lookupCount = 1
	}
	c.inodes[id] = inode
	c.paths[key] = id
	inode.lruElement = c.lru.PushFront(inode)
	c.evictLocked()
	return inode
}

// materialize returns the inode's attributes, computing them via compute if the
// inode has not yet been materialized.
func (c *InodeCache) materialize(id fuseops.InodeID, compute func(*inode) fuseops.InodeAttributes) (fuseops.InodeAttributes, bool) {
	c.mu.Lock()
	inode, ok := c.inodes[id]
	if !ok {
		c.mu.Unlock()
		return fuseops.InodeAttributes{}, false
	}
	if inode.materialized {
		attrs := inode.attributes
		c.lru.MoveToFront(inode.lruElement)
		c.mu.Unlock()
		return attrs, true
	}
	c.mu.Unlock()

	attrs := compute(inode)

	c.mu.Lock()
	if !inode.materialized {
		inode.attributes = attrs
		inode.materialized = true
	}
	c.lru.MoveToFront(inode.lruElement)
	result := inode.attributes
	c.mu.Unlock()
	return result, true
}

// forgetInode decrements the inode's reference count by n and evicts it once it
// is no longer referenced.
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
	delete(c.inodes, id)
	delete(c.paths, pathKey{entry.Namespace, entry.Path})
	if inode.lruElement != nil {
		c.lru.Remove(inode.lruElement)
		inode.lruElement = nil
	}
}

// evictLocked removes unreferenced, least-recently-used inodes until the cache is
// within its limit. Referenced inodes and the root inode are never evicted.
func (c *InodeCache) evictLocked() {
	for len(c.inodes) > c.maxInodes {
		el := c.lru.Back()
		if el == nil {
			return
		}
		inode := el.Value.(*inode)
		if inode.lookupCount != 0 || inode.id == fuseops.RootInodeID {
			return
		}
		c.removeLocked(inode.id, inode)
	}
}
