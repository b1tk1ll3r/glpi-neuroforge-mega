package store

import (
	"container/list"
	"sync"

	"neuroforge/internal/core"
)

type cacheEntry struct {
	id    string
	mem   core.Memory
	bytes int64
}

type MemoryPageCache struct {
	mu        sync.Mutex
	enabled   bool
	maxBytes  int64
	bytes     int64
	hits      uint64
	misses    uint64
	evictions uint64
	ll        *list.List
	items     map[string]*list.Element
}

func newMemoryPageCache(enabled bool, maxBytes int64) *MemoryPageCache {
	if maxBytes <= 0 {
		maxBytes = 256 << 20
	}
	return &MemoryPageCache{enabled: enabled, maxBytes: maxBytes, ll: list.New(), items: map[string]*list.Element{}}
}

func memoryApproxBytes(m core.Memory) int64 {
	// Include a conservative fixed overhead plus the dominant variable payloads.
	n := int64(256 + len(m.Text) + len(m.ID) + len(m.Kind) + len(m.MemoryType) + len(m.SessionID) + len(m.TruthKey))
	n += int64(len(m.Vector)) * 4
	for _, t := range m.Tags {
		n += int64(len(t) + 16)
	}
	return n
}

func (c *MemoryPageCache) Reconfigure(enabled bool, maxBytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled = enabled
	if maxBytes > 0 {
		c.maxBytes = maxBytes
	}
	if !enabled {
		c.ll.Init()
		c.items = map[string]*list.Element{}
		c.bytes = 0
		return
	}
	c.evictLocked()
}

func (c *MemoryPageCache) Get(id string) (core.Memory, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		c.misses++
		return core.Memory{}, false
	}
	el, ok := c.items[id]
	if !ok {
		c.misses++
		return core.Memory{}, false
	}
	c.hits++
	c.ll.MoveToFront(el)
	return cloneMemory(el.Value.(*cacheEntry).mem), true
}

func (c *MemoryPageCache) Put(m core.Memory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled || m.ID == "" {
		return
	}
	cp := cloneMemory(m)
	sz := memoryApproxBytes(cp)
	if el, ok := c.items[m.ID]; ok {
		old := el.Value.(*cacheEntry)
		c.bytes -= old.bytes
		old.mem, old.bytes = cp, sz
		c.bytes += sz
		c.ll.MoveToFront(el)
	} else {
		el := c.ll.PushFront(&cacheEntry{id: m.ID, mem: cp, bytes: sz})
		c.items[m.ID] = el
		c.bytes += sz
	}
	c.evictLocked()
}

func (c *MemoryPageCache) Delete(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		ent := el.Value.(*cacheEntry)
		c.bytes -= ent.bytes
		delete(c.items, id)
		c.ll.Remove(el)
	}
}

func (c *MemoryPageCache) evictLocked() {
	for c.maxBytes > 0 && c.bytes > c.maxBytes {
		el := c.ll.Back()
		if el == nil {
			break
		}
		ent := el.Value.(*cacheEntry)
		c.bytes -= ent.bytes
		delete(c.items, ent.id)
		c.ll.Remove(el)
		c.evictions++
	}
}

func (c *MemoryPageCache) Stats() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{
		"enabled": c.enabled, "max_bytes": c.maxBytes, "bytes": c.bytes,
		"entries": len(c.items), "hits": c.hits, "misses": c.misses, "evictions": c.evictions,
	}
}
