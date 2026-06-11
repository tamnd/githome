package domain

import (
	"container/list"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/tamnd/githome/git"
)

// search_corpus.go bounds the cost of unindexed code search. The scan used to
// re-read up to codeScanLimit blobs from git, lowercase every byte, and throw
// the work away at the end of each request, which made a repeated search (or a
// hostile one) cost a full repository read every time. The corpus a scan needs
// is a pure function of the repository's head commit, and commits are
// immutable, so the lowered corpus is cached under (repoPK, headSHA): a push
// moves the head to a new sha and naturally mints a new key, and the old entry
// ages out by LRU. This is the "cache per (repo, ref) corpus" floor from review
// 2005/03 R03-0A; a background index can replace it without changing callers.

const (
	// corpusCacheMaxBytes bounds the whole cache.
	corpusCacheMaxBytes = 64 << 20
	// corpusMaxEntryBytes bounds one repository's corpus; a repo whose lowered
	// text exceeds it is scanned per request rather than monopolizing the cache.
	corpusMaxEntryBytes = 16 << 20
	// corpusMaxBlobBytes caps how much of one blob enters the corpus. Code
	// search matches source files; a giant text artifact contributes its head.
	corpusMaxBlobBytes = 512 << 10
)

// corpusDoc is one searchable file: its tree facts plus the lowercased path
// and text the term matcher runs over. lowerText is empty for a binary or
// unreadable blob, which can still match by path.
type corpusDoc struct {
	path      string
	name      string
	sha       string
	lowerPath string
	lowerText string
}

// repoCorpus is one repository's searchable snapshot at a head commit.
// truncated reports that the build stopped at the blob ceiling, which the
// search surfaces as incomplete results.
type repoCorpus struct {
	docs      []corpusDoc
	truncated bool
	bytes     int
}

// corpusCache is a byte-bounded LRU over repoCorpus values, the same shape as
// the git layer's diff cache. Entries are immutable once stored.
type corpusCache struct {
	mu    sync.Mutex
	max   int
	cur   int
	ll    *list.List
	items map[string]*list.Element
	hits  uint64
}

type corpusEntry struct {
	key    string
	corpus *repoCorpus
}

func newCorpusCache(maxBytes int) *corpusCache {
	return &corpusCache{max: maxBytes, ll: list.New(), items: map[string]*list.Element{}}
}

// corpusKey addresses a corpus by repository and head commit. The sha pins the
// content: a force-push or a new commit changes the key, never the value.
func corpusKey(pk int64, headSHA string) string {
	return strconv.FormatInt(pk, 10) + ":" + headSHA
}

func (c *corpusCache) get(key string) (*repoCorpus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	c.hits++
	return el.Value.(*corpusEntry).corpus, true
}

func (c *corpusCache) put(key string, corpus *repoCorpus) {
	cost := corpus.bytes + len(key)
	if cost > corpusMaxEntryBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; ok {
		return
	}
	c.items[key] = c.ll.PushFront(&corpusEntry{key: key, corpus: corpus})
	c.cur += cost
	for c.cur > c.max {
		el := c.ll.Back()
		if el == nil {
			break
		}
		ent := el.Value.(*corpusEntry)
		c.ll.Remove(el)
		delete(c.items, ent.key)
		c.cur -= ent.corpus.bytes + len(ent.key)
	}
}

// repoCorpus returns the repository's searchable corpus at its current head,
// from cache when the head sha is known and the corpus was built before. A
// repository whose head cannot be resolved (empty repo) yields nil.
func (s *SearchService) repoCorpus(repo *Repo) *repoCorpus {
	head, err := s.repos.DefaultBranchRef(repo)
	cacheable := err == nil && head.Commit != "" && s.corpus != nil
	var key string
	if cacheable {
		key = corpusKey(repo.PK, head.Commit)
		if c, ok := s.corpus.get(key); ok {
			return c
		}
	}
	c := s.buildCorpus(repo)
	if cacheable && c != nil {
		s.corpus.put(key, c)
	}
	return c
}

// buildCorpus walks the head tree and reads every blob once, lowering paths and
// text up front so matching is pure string work. The walk stops at
// codeScanLimit blobs, the same ceiling the uncached scan enforced.
func (s *SearchService) buildCorpus(repo *Repo) *repoCorpus {
	tree, err := s.repos.GetTree(repo, repo.DefaultBranch, true)
	if err != nil {
		// An empty or unreadable repository contributes no code matches.
		return nil
	}
	c := &repoCorpus{}
	for _, e := range tree.Entries {
		if e.Type != git.ObjectBlob {
			continue
		}
		if len(c.docs) >= codeScanLimit {
			c.truncated = true
			break
		}
		d := corpusDoc{
			path:      e.Path,
			name:      path.Base(e.Path),
			sha:       e.SHA,
			lowerPath: strings.ToLower(e.Path),
		}
		if blob, err := s.repos.GetBlob(repo, e.SHA); err == nil && isText(blob.Content) {
			content := blob.Content
			if len(content) > corpusMaxBlobBytes {
				content = content[:corpusMaxBlobBytes]
			}
			d.lowerText = strings.ToLower(string(content))
		}
		c.bytes += len(d.path) + len(d.name) + len(d.sha) + len(d.lowerPath) + len(d.lowerText)
		c.docs = append(c.docs, d)
	}
	return c
}

// matchDoc reports whether the doc satisfies every term, by path or by text.
// No terms means every file matches, the way an empty code query lists the
// scoped tree.
func matchDoc(d corpusDoc, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(d.lowerPath, t) && !strings.Contains(d.lowerText, t) {
			return false
		}
	}
	return true
}
