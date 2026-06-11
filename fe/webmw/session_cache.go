package webmw

import (
	"sync"
	"time"

	"github.com/tamnd/githome/fe/view"
)

// session_cache.go bounds the session middleware's per-request cost. Every
// signed-in page and htmx fragment used to pay one uncached viewer lookup (a
// database read) before any handler ran, the per-request floor named in review
// 2005/03 R03-13. The viewer model changes rarely, so a short TTL cache keyed
// by user primary key absorbs the steady state: a lookup answers from memory
// for the TTL window and is re-read after. The TTL is deliberately small so a
// rename or avatar change shows within a minute, and the two control points
// that must not wait even that long bypass it: Clear (logout) and Issue
// (login) drop the entry, so a signed-out session is anonymous on the very
// next request.

const (
	// viewerCacheTTL is how long a resolved viewer answers from memory. Small
	// on purpose: profile edits surface within this window without any write
	// path having to know about the cache.
	viewerCacheTTL = 45 * time.Second
	// viewerCacheMaxEntries bounds the map. One entry per distinct signed-in
	// user inside one TTL window, so the bound is generous; hitting it clears
	// the cache rather than growing without limit, and the next requests
	// simply re-read.
	viewerCacheMaxEntries = 16384
)

// viewerEntry is one cached viewer and the moment it stops being trusted.
type viewerEntry struct {
	v   *view.Viewer
	exp time.Time
}

// viewerCache is a TTL map from user primary key to the resolved viewer. It is
// value-embedded in Sessions and shared by every request.
type viewerCache struct {
	mu    sync.Mutex
	items map[int64]viewerEntry
}

func (c *viewerCache) get(pk int64, now time.Time) (*view.Viewer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[pk]
	if !ok {
		return nil, false
	}
	if now.After(e.exp) {
		delete(c.items, pk)
		return nil, false
	}
	return e.v, true
}

func (c *viewerCache) put(pk int64, v *view.Viewer, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= viewerCacheMaxEntries {
		c.items = map[int64]viewerEntry{}
	}
	c.items[pk] = viewerEntry{v: v, exp: now.Add(viewerCacheTTL)}
}

func (c *viewerCache) drop(pk int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, pk)
}
