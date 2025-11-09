package cache

import (
	"context"
	"container/list"
	"errors"
	"sync"
	"time"
)

// Entry represents cached content.
type Entry struct {
	Key         string
	Body        []byte
	ContentType string
	TTL         time.Duration
	StoredAt    time.Time
}

// alive reports whether entry is still valid.
func (e *Entry) alive(now time.Time) bool {
	if e.TTL <= 0 {
		return true
	}
	return now.Sub(e.StoredAt) < e.TTL
}

// ErrNotFound indicates absence or expiration.
var ErrNotFound = errors.New("cache: not found")

// LRU implements an in-memory TTL aware LRU cache.
type LRU struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	ll       *list.List
}

type kv struct {
	key string
	val Entry
}

// NewLRU creates cache with maximum number of entries.
func NewLRU(capacity int) *LRU {
	if capacity <= 0 {
		capacity = 128
	}
	return &LRU{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		ll:       list.New(),
	}
}

// Get returns entry if present and fresh.
func (c *LRU) Get(key string) (Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, ok := c.items[key]; ok {
		c.ll.MoveToFront(ele)
		entry := ele.Value.(kv).val
		if entry.alive(time.Now()) {
			return entry, nil
		}
		c.removeElement(ele)
	}
	return Entry{}, ErrNotFound
}

// Set inserts entry with TTL.
func (c *LRU) Set(entry Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, ok := c.items[entry.Key]; ok {
		c.ll.MoveToFront(ele)
		ele.Value = kv{key: entry.Key, val: entry}
		return
	}
	ele := c.ll.PushFront(kv{key: entry.Key, val: entry})
	c.items[entry.Key] = ele
	if c.ll.Len() > c.capacity {
		c.evictOldest()
	}
}

// Delete removes entry.
func (c *LRU) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, ok := c.items[key]; ok {
		c.removeElement(ele)
	}
}

func (c *LRU) removeElement(ele *list.Element) {
	c.ll.Remove(ele)
	delete(c.items, ele.Value.(kv).key)
}

func (c *LRU) evictOldest() {
	if ele := c.ll.Back(); ele != nil {
		c.removeElement(ele)
	}
}

// Cleanup purges expired entries.
func (c *LRU) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	removed := 0
	for key, ele := range c.items {
		entry := ele.Value.(kv).val
		if !entry.alive(now) {
			c.removeElement(ele)
			delete(c.items, key)
			removed++
		}
	}
	return removed
}

// StartJanitor launches background cleanup loop until context is done.
func (c *LRU) StartJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.Cleanup()
			}
		}
	}()
}

