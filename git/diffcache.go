package git

import (
	"container/list"
	"strconv"
	"sync"
)

// diffcache.go holds the parsed-diff cache behind ChangedFiles. A diff between
// two commits is a pure function of their ids, so a (pk, base, head) entry can
// never go stale: a force-push mints new ids and therefore new keys. The PR
// Files page and the review-thread indexer both ask for the same range in one
// request, and the compare page re-asks on every reload, so even a small cache
// turns the second full git-diff subprocess into a map read.

// diffCacheMaxBytes bounds the cache by the patch text it holds, the part that
// dominates an entry's footprint. 32 MiB holds plenty of warm ranges without
// letting one busy server hoard diffs.
const diffCacheMaxBytes = 32 << 20

// diffCacheMaxEntryBytes skips caching a single huge diff: a multi-megabyte
// vendored-dependency change would evict everything else for one range that is
// unlikely to be re-read before it scrolls out anyway.
const diffCacheMaxEntryBytes = 8 << 20

// diffCache is a byte-bounded LRU of parsed diffs keyed by (pk, base, head).
type diffCache struct {
	mu    sync.Mutex
	max   int64
	size  int64
	order *list.List               // front = most recent; values are *diffEntry
	byKey map[string]*list.Element // key -> element in order
	hits  uint64                   // test observability
}

type diffEntry struct {
	key   string
	files []FileChange
	cost  int64
}

func newDiffCache(maxBytes int64) *diffCache {
	return &diffCache{max: maxBytes, order: list.New(), byKey: map[string]*list.Element{}}
}

func diffKey(pk int64, base, head SHA) string {
	return strconv.FormatInt(pk, 10) + ":" + base + ":" + head
}

// get returns the cached parsed diff for key, or nil. The returned slice is
// shared: callers may re-slice it but never write through it, the same contract
// a fresh parse has in practice (every consumer is read-only).
func (c *diffCache) get(key string) []FileChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		return nil
	}
	c.order.MoveToFront(el)
	c.hits++
	return el.Value.(*diffEntry).files
}

// put stores files under key, evicting from the cold end until the byte budget
// holds. An entry over the per-entry cap is not stored at all.
func (c *diffCache) put(key string, files []FileChange) {
	cost := diffCost(files)
	if cost > diffCacheMaxEntryBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		c.order.MoveToFront(el)
		old := el.Value.(*diffEntry)
		c.size += cost - old.cost
		old.files, old.cost = files, cost
	} else {
		el := c.order.PushFront(&diffEntry{key: key, files: files, cost: cost})
		c.byKey[key] = el
		c.size += cost
	}
	for c.size > c.max {
		tail := c.order.Back()
		if tail == nil {
			break
		}
		e := tail.Value.(*diffEntry)
		c.order.Remove(tail)
		delete(c.byKey, e.key)
		c.size -= e.cost
	}
}

// diffCost approximates an entry's memory footprint by the strings it carries.
func diffCost(files []FileChange) int64 {
	var n int64 = 64 // key + bookkeeping
	for i := range files {
		n += int64(len(files[i].Patch) + len(files[i].Path) + len(files[i].PrevPath) + 128)
	}
	return n
}

// isFullSHA reports whether s is a full 40-hex object id, the only shape a
// content-addressed cache key may carry. A branch or tag name moves under the
// same spelling, so it never keys a cache entry.
func isFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
