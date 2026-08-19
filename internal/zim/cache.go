package zim

import (
	"container/list"
	"sync"
)

const MAX_CLUSTER_CACHE_SIZE = 64 << 20 // 64Mb

type clusterData struct {
	offsets []uint64
	blobs   []byte
}

func (c *clusterData) size() uint64 {
	return uint64(len(c.blobs)) + uint64(len(c.offsets))*8
}

// Bounded LRU cache of cluster data, keyed by cluster number
type clusterCache struct {
	mu       sync.Mutex
	maxBytes uint64
	curBytes uint64
	entries  map[uint32]*list.Element
	lru      *list.List
}

type clusterCacheEntry struct {
	clusterNumber uint32
	data          clusterData
}

func newClusterCache(maxBytes uint64) *clusterCache {
	return &clusterCache{
		maxBytes: maxBytes,
		entries:  make(map[uint32]*list.Element),
		lru:      list.New(),
	}
}

// Get the data stored at cluster number
func (c *clusterCache) get(clusterNumber uint32) (clusterData, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.entries[clusterNumber]; ok {
		c.lru.MoveToFront(el)
		return el.Value.(*clusterCacheEntry).data, true
	}
	return clusterData{}, false
}

// cache data for clusterNumber
func (c *clusterCache) put(clusterNumber uint32, data clusterData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.entries[clusterNumber]; ok {
		entry := el.Value.(*clusterCacheEntry)
		c.curBytes -= entry.data.size()
		entry.data = data
		c.curBytes += data.size()
		c.lru.MoveToFront(el)
		c.evictLocked()
		return
	}

	entry := &clusterCacheEntry{clusterNumber: clusterNumber, data: data}
	el := c.lru.PushFront(entry)
	c.entries[clusterNumber] = el
	c.curBytes += data.size()
	c.evictLocked()
}

// Removes least recently used entries until the cache is under it's byte budget.
// Caller must hold c.mu
func (c *clusterCache) evictLocked() {
	if c.maxBytes == 0 {
		return
	}
	for c.curBytes > c.maxBytes && c.lru.Len() > 0 {
		el := c.lru.Back()
		entry := el.Value.(*clusterCacheEntry)
		c.lru.Remove(el)
		delete(c.entries, entry.clusterNumber)
		c.curBytes -= entry.data.size()
	}
}
