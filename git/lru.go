package git

import (
	"container/list"
	"sync"
)

// costLRU is a byte-bounded LRU keyed by string, the shape the content-addressed
// subprocess caches share (blame, commit patch). A key embeds full object ids,
// so an entry can never go stale; the budget is measured by a caller-supplied
// cost function over the value, the part that dominates an entry's footprint.
// Values are treated as immutable once stored: get returns them shared.
type costLRU[V any] struct {
	mu       sync.Mutex
	max      int64
	maxEntry int64
	size     int64
	order    *list.List               // front = most recent; values are *lruEntry[V]
	byKey    map[string]*list.Element // key -> element in order
	cost     func(V) int64
	hits     uint64 // test observability
}

type lruEntry[V any] struct {
	key  string
	val  V
	cost int64
}

// newCostLRU builds a cache holding at most maxBytes of value cost. An entry
// costing more than maxEntryBytes is not stored at all, so one huge value
// cannot evict everything else for a key unlikely to be re-read.
func newCostLRU[V any](maxBytes, maxEntryBytes int64, cost func(V) int64) *costLRU[V] {
	return &costLRU[V]{
		max:      maxBytes,
		maxEntry: maxEntryBytes,
		order:    list.New(),
		byKey:    map[string]*list.Element{},
		cost:     cost,
	}
}

func (c *costLRU[V]) get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	c.hits++
	return el.Value.(*lruEntry[V]).val, true
}

func (c *costLRU[V]) put(key string, v V) {
	cost := c.cost(v) + int64(len(key)) + 64
	if cost > c.maxEntry {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		c.order.MoveToFront(el)
		old := el.Value.(*lruEntry[V])
		c.size += cost - old.cost
		old.val, old.cost = v, cost
	} else {
		el := c.order.PushFront(&lruEntry[V]{key: key, val: v, cost: cost})
		c.byKey[key] = el
		c.size += cost
	}
	for c.size > c.max {
		tail := c.order.Back()
		if tail == nil {
			break
		}
		e := tail.Value.(*lruEntry[V])
		c.order.Remove(tail)
		delete(c.byKey, e.key)
		c.size -= e.cost
	}
}
