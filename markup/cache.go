package markup

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"html/template"
	"sync"
)

// cache.go is the rendered-fragment LRU implementation/03 section 6.4 keys on
// the version constants in version.go: rendered Markdown keyed by
// sha256(render context, source, markupVersion) and highlighted lines keyed by
// sha256(code, lang, highlighterVersion). Both inputs are content-addressed
// (a comment body is immutable between edits, a blob's bytes are its identity),
// so entries never go stale and a version bump transparently re-keys everything.
// The cached value is the already-sanitized template.HTML the trust boundary
// produced, so serving it again does not re-open that boundary.

// fragCacheMaxBytes bounds the cache by the HTML it holds. READMEs, comment
// bodies, and highlighted blobs are each at most a few hundred KB after the
// display caps, so 48 MiB holds thousands of warm fragments.
const fragCacheMaxBytes = 48 << 20

// fragCacheMaxEntryBytes skips caching one huge fragment rather than letting it
// evict a page's worth of small ones.
const fragCacheMaxEntryBytes = 2 << 20

// fragKey is a sha256 over every output-affecting input plus the version
// constant, so the map never needs the inputs themselves.
type fragKey [sha256.Size]byte

// fragCache is a byte-bounded LRU of rendered fragments. A value is either a
// template.HTML (markdown) or a []template.HTML (highlighted lines); cost
// tracks its HTML byte size. It is safe for concurrent use.
type fragCache struct {
	mu    sync.Mutex
	max   int64
	size  int64
	order *list.List
	byKey map[fragKey]*list.Element
	hits  uint64 // test and debug observability
}

type fragEntry struct {
	key  fragKey
	val  any
	cost int64
}

func newFragCache(maxBytes int64) *fragCache {
	return &fragCache{max: maxBytes, order: list.New(), byKey: map[fragKey]*list.Element{}}
}

func (c *fragCache) get(key fragKey) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits++
	return el.Value.(*fragEntry).val, true
}

func (c *fragCache) put(key fragKey, val any, cost int64) {
	if cost > fragCacheMaxEntryBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		c.order.MoveToFront(el)
		e := el.Value.(*fragEntry)
		c.size += cost - e.cost
		e.val, e.cost = val, cost
	} else {
		el := c.order.PushFront(&fragEntry{key: key, val: val, cost: cost})
		c.byKey[key] = el
		c.size += cost
	}
	for c.size > c.max {
		tail := c.order.Back()
		if tail == nil {
			break
		}
		e := tail.Value.(*fragEntry)
		c.order.Remove(tail)
		delete(c.byKey, e.key)
		c.size -= e.cost
	}
}

// len reports the entry count, for tests.
func (c *fragCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byKey)
}

// markdownKey hashes every input that selects rendered-markdown output: the
// version constant, the render mode, the repo identity, the ref and path the
// relative-link rewriting reads, and the source itself. Callers must only key a
// render whose RenderContext carries no Resolve closure; with one present the
// output depends on caller state the hash cannot see.
func markdownKey(rc RenderContext, src []byte) fragKey {
	h := sha256.New()
	var hdr [16]byte
	binary.LittleEndian.PutUint64(hdr[0:], uint64(markupVersion))
	binary.LittleEndian.PutUint64(hdr[8:], uint64(rc.Mode))
	h.Write(hdr[:])
	if rc.Repo == nil {
		h.Write([]byte{0})
	} else {
		h.Write([]byte{1})
		var id [8]byte
		binary.LittleEndian.PutUint64(id[:], uint64(rc.Repo.ID))
		h.Write(id[:])
		writeField(h, rc.Repo.Owner)
		writeField(h, rc.Repo.Name)
	}
	writeField(h, rc.Ref)
	writeField(h, rc.Path)
	h.Write(src)
	var key fragKey
	h.Sum(key[:0])
	return key
}

// highlightKey hashes the highlighted-cell inputs: the version constant, the
// grammar label, and the code bytes (the blob's content is its identity, the
// same thing its blob SHA names).
func highlightKey(code []byte, lang string) fragKey {
	h := sha256.New()
	var v [8]byte
	binary.LittleEndian.PutUint64(v[:], uint64(highlighterVersion))
	h.Write(v[:])
	writeField(h, lang)
	h.Write(code)
	var key fragKey
	h.Sum(key[:0])
	return key
}

// writeField writes a length-prefixed string so adjacent fields can never
// alias ("a"+"bc" vs "ab"+"c").
func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(s)))
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(s))
}

// linesCost is the byte cost of a highlighted-lines value.
func linesCost(lines []template.HTML) int64 {
	var n int64 = 64
	for _, l := range lines {
		n += int64(len(l)) + 16
	}
	return n
}
