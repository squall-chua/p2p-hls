package app

import "sync"

// segCache holds verified Segment bytes for the active viewed party, keyed by
// Segment index. Bounded by the swarm window via retain().
type segCache struct {
	mu   sync.Mutex
	data map[int][]byte
}

func newSegCache() *segCache { return &segCache{data: map[int][]byte{}} }

func (c *segCache) put(idx int, b []byte) {
	c.mu.Lock()
	c.data[idx] = b
	c.mu.Unlock()
}

func (c *segCache) get(idx int) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.data[idx]
	return b, ok
}

// retain drops cached Segments outside the inclusive window [min,max].
func (c *segCache) retain(min, max int) {
	c.mu.Lock()
	for idx := range c.data {
		if idx < min || idx > max {
			delete(c.data, idx)
		}
	}
	c.mu.Unlock()
}
