package git

import (
	"bufio"
	"container/list"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// objInfo caches the result of a cat-file --batch-check lookup for one object.
// Keys are the 40-hex SHA (globally unique and immutable).
type objInfo struct {
	typ     string // "commit", "tree", "blob", "tag"
	size    int64
	missing bool
}

// catProc is a single long-lived git cat-file --batch-check process bound to
// one bare repository directory. All queries are serialized through the
// process's stdin/stdout; mu enforces that.
type catProc struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	w   io.WriteCloser
	r   *bufio.Reader
	// dead is set when an I/O error occurs; callers see the error and the pool
	// evicts the entry so the next call recreates the process.
	dead bool
}

func newCatProc(gitBin, dir string) (*catProc, error) {
	cmd := exec.Command(gitBin, "--git-dir", dir, "cat-file", "--batch-check")
	cmd.Env = baseEnv()
	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &catProc{cmd: cmd, w: w, r: bufio.NewReader(stdout)}, nil
}

// lookup sends sha to the process and parses the response.
func (p *catProc) lookup(sha string) (objInfo, error) {
	if _, err := fmt.Fprintln(p.w, sha); err != nil {
		p.dead = true
		return objInfo{}, err
	}
	line, err := p.r.ReadString('\n')
	if err != nil {
		p.dead = true
		return objInfo{}, err
	}
	line = strings.TrimRight(line, "\n")
	// "<sha> missing" or "<sha> <type> <size>"
	parts := strings.Fields(line)
	if len(parts) == 2 && parts[1] == "missing" {
		return objInfo{missing: true}, nil
	}
	if len(parts) == 3 {
		sz, _ := strconv.ParseInt(parts[2], 10, 64)
		return objInfo{typ: parts[1], size: sz}, nil
	}
	p.dead = true
	return objInfo{}, fmt.Errorf("git cat-file: unexpected %q", line)
}

func (p *catProc) close() {
	_ = p.w.Close()
	_ = p.cmd.Wait()
}

// catFilePool maintains up to maxProcs long-lived cat-file --batch-check
// processes (one per repo pk), evicting the least-recently-used when full.
// All exported calls are safe for concurrent use.
type catFilePool struct {
	bin      string
	mu       sync.Mutex
	procs    map[int64]*catProc
	lru      *list.List            // front = MRU, back = LRU
	lruIdx   map[int64]*list.Element
	maxProcs int
}

func newCatFilePool(bin string, max int) *catFilePool {
	if max <= 0 {
		max = 64
	}
	return &catFilePool{
		bin:      bin,
		procs:    make(map[int64]*catProc),
		lru:      list.New(),
		lruIdx:   make(map[int64]*list.Element),
		maxProcs: max,
	}
}

// lookup queries the object identified by sha in the bare repo at dir (pk is
// used as the pool key). On process death the entry is evicted and the error
// propagates; the next call recreates the process.
func (pool *catFilePool) lookup(dir string, pk int64, sha string) (objInfo, error) {
	p := pool.acquire(pk, dir)
	if p == nil {
		return objInfo{}, fmt.Errorf("git cat-file: failed to start process for repo %d", pk)
	}
	p.mu.Lock()
	info, err := p.lookup(sha)
	dead := p.dead
	p.mu.Unlock()
	if dead {
		pool.evict(pk)
	}
	return info, err
}

// acquire returns the process for pk, creating it if necessary.
func (pool *catFilePool) acquire(pk int64, dir string) *catProc {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if p, ok := pool.procs[pk]; ok {
		pool.touch(pk)
		return p
	}
	// Evict LRU entries until we're below cap.
	for len(pool.procs) >= pool.maxProcs {
		e := pool.lru.Back()
		if e == nil {
			break
		}
		old := e.Value.(int64)
		pool.lru.Remove(e)
		delete(pool.lruIdx, old)
		if p, ok := pool.procs[old]; ok {
			p.close()
			delete(pool.procs, old)
		}
	}
	p, err := newCatProc(pool.bin, dir)
	if err != nil {
		return nil
	}
	pool.procs[pk] = p
	el := pool.lru.PushFront(pk)
	pool.lruIdx[pk] = el
	return p
}

// touch marks pk as most-recently used.
func (pool *catFilePool) touch(pk int64) {
	if el, ok := pool.lruIdx[pk]; ok {
		pool.lru.MoveToFront(el)
	}
}

// evict removes the process for pk (called after process death).
func (pool *catFilePool) evict(pk int64) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if p, ok := pool.procs[pk]; ok {
		p.close()
		delete(pool.procs, pk)
	}
	if el, ok := pool.lruIdx[pk]; ok {
		pool.lru.Remove(el)
		delete(pool.lruIdx, pk)
	}
}

// close shuts down all live processes.
func (pool *catFilePool) close() {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for pk, p := range pool.procs {
		p.close()
		delete(pool.procs, pk)
	}
	pool.lru.Init()
	for k := range pool.lruIdx {
		delete(pool.lruIdx, k)
	}
}

// objCache is a bounded SHA-keyed cache of object info. Because git SHAs are
// content addresses, a cached entry is permanently valid: the same SHA always
// names the same object. Large blobs are excluded to bound memory growth.
// Eviction is LRU; entry count (not byte size) bounds the cache since type
// strings are short and object sizes are metadata, not content.
const (
	objCacheMaxEntries = 4096
	objCacheMaxBlobBytes = 10 << 20 // blobs > 10 MB are not cached
)

type objCache struct {
	mu      sync.Mutex
	list    *list.List
	entries map[string]*list.Element
	max     int
}

type cacheEntry struct {
	sha  string
	info objInfo
}

func newObjCache(max int) *objCache {
	if max <= 0 {
		max = objCacheMaxEntries
	}
	return &objCache{list: list.New(), entries: make(map[string]*list.Element), max: max}
}

func (c *objCache) get(sha string) (objInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[sha]
	if !ok {
		return objInfo{}, false
	}
	c.list.MoveToFront(el)
	return el.Value.(*cacheEntry).info, true
}

func (c *objCache) put(sha string, info objInfo) {
	// Don't cache large blobs.
	if !info.missing && info.typ == "blob" && info.size > objCacheMaxBlobBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[sha]; ok {
		c.list.MoveToFront(el)
		el.Value.(*cacheEntry).info = info
		return
	}
	for c.list.Len() >= c.max {
		oldest := c.list.Back()
		if oldest == nil {
			break
		}
		c.list.Remove(oldest)
		delete(c.entries, oldest.Value.(*cacheEntry).sha)
	}
	el := c.list.PushFront(&cacheEntry{sha: sha, info: info})
	c.entries[sha] = el
}
