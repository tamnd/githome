package git

import (
	"container/list"
	"sync"

	gogit "github.com/go-git/go-git/v5"
)

// A fresh go-git handle parses the pack index lazily, so on a large repository
// every point read through a cold handle pays a multi-hundred-megabyte .idx
// parse before touching the object it wants. The cache below keeps warm handles
// around between requests.
//
// go-git's filesystem ObjectStorage is not safe for concurrent use (the lazy
// index map and packfile readers have unsynchronized state), so a cached handle
// is never shared: acquire hands a handle to exactly one caller and release
// puts it back when the request is done. Object content is immutable, but a
// push can add new packfiles a warm handle's index map would never see, so the
// push path bumps the repository's generation, which drops the idle handles
// and refuses returns from handles acquired before the push.
const (
	// repoCacheMaxRepos bounds how many repositories keep warm handles at once;
	// least recently used repositories are evicted first.
	repoCacheMaxRepos = 64
	// repoCacheHandlesPerRepo bounds the idle handles kept per repository.
	// Concurrent requests beyond this simply open fresh handles.
	repoCacheHandlesPerRepo = 4
)

// repoCache is a bounded LRU of warm go-git handles keyed by repository
// directory. Safe for concurrent use.
type repoCache struct {
	mu      sync.Mutex
	max     int
	perRepo int
	entries map[string]*repoCacheEntry
	order   *list.List // front = most recently used; element values are *repoCacheEntry
}

type repoCacheEntry struct {
	dir  string
	elem *list.Element
	gen  uint64
	idle []*gogit.Repository
}

func newRepoCache(maxRepos, perRepo int) *repoCache {
	return &repoCache{
		max:     maxRepos,
		perRepo: perRepo,
		entries: make(map[string]*repoCacheEntry),
		order:   list.New(),
	}
}

// acquire checks a warm handle out of the cache for exclusive use, creating the
// repository's cache entry if this is its first sighting. The returned
// generation must accompany the handle back into release. ok is false when no
// idle handle exists and the caller must open a fresh one.
func (c *repoCache) acquire(dir string) (h *gogit.Repository, gen uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.touch(dir)
	gen = e.gen
	if n := len(e.idle); n > 0 {
		h = e.idle[n-1]
		e.idle[n-1] = nil
		e.idle = e.idle[:n-1]
		return h, gen, true
	}
	return nil, gen, false
}

// release returns a handle to the cache. The handle is dropped instead when the
// repository was invalidated after the acquire (the generation moved on), when
// the entry was evicted, or when the idle list is already full.
func (c *repoCache) release(dir string, gen uint64, h *gogit.Repository) {
	if h == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[dir]
	if e == nil || e.gen != gen || len(e.idle) >= c.perRepo {
		return
	}
	e.idle = append(e.idle, h)
	c.order.MoveToFront(e.elem)
}

// invalidate drops the repository's idle handles and bumps its generation so
// handles checked out before the call are not re-cached. The push path calls it
// after receive-pack may have written new packfiles.
func (c *repoCache) invalidate(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[dir]; e != nil {
		e.gen++
		e.idle = nil
	}
}

// touch returns the entry for dir as the most recently used, creating it (and
// evicting the least recently used entry when full) as needed. Callers hold mu.
func (c *repoCache) touch(dir string) *repoCacheEntry {
	if e := c.entries[dir]; e != nil {
		c.order.MoveToFront(e.elem)
		return e
	}
	for len(c.entries) >= c.max {
		back := c.order.Back()
		if back == nil {
			break
		}
		evicted := back.Value.(*repoCacheEntry)
		c.order.Remove(back)
		delete(c.entries, evicted.dir)
	}
	e := &repoCacheEntry{dir: dir}
	e.elem = c.order.PushFront(e)
	c.entries[dir] = e
	return e
}
