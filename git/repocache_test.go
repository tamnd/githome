package git

import (
	"fmt"
	"testing"

	gogit "github.com/go-git/go-git/v5"
)

// openBare initializes a bare repository for pk under the store and returns the
// store, failing the test on error.
func newStoreWithRepo(t *testing.T, pk int64) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	t.Cleanup(s.Close)
	if _, err := s.Init(pk); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

func TestOpenReusesReleasedHandle(t *testing.T) {
	s := newStoreWithRepo(t, 7)

	r1, err := s.Open(7)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h1 := r1.repo
	r1.Release()
	if r1.repo != nil {
		t.Fatal("Release must detach the handle from the Repo")
	}

	r2, err := s.Open(7)
	if err != nil {
		t.Fatalf("Open after release: %v", err)
	}
	if r2.repo != h1 {
		t.Fatal("expected the released handle to be reused by the next Open")
	}
	r2.Release()
}

func TestConcurrentOpensNeverShareAHandle(t *testing.T) {
	s := newStoreWithRepo(t, 7)

	r1, err := s.Open(7)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r2, err := s.Open(7)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if r1.repo == r2.repo {
		t.Fatal("two checked-out Repos share one go-git handle; go-git handles are not concurrency-safe")
	}
	r1.Release()
	r2.Release()
}

func TestInvalidateRepoDropsWarmHandles(t *testing.T) {
	s := newStoreWithRepo(t, 7)

	r1, err := s.Open(7)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h1 := r1.repo
	r1.Release()

	// Simulate a push: the cached handle must not survive.
	s.InvalidateRepo(7)

	r2, err := s.Open(7)
	if err != nil {
		t.Fatalf("Open after invalidate: %v", err)
	}
	if r2.repo == h1 {
		t.Fatal("InvalidateRepo left a stale handle in the cache")
	}

	// A handle checked out before the invalidation must be refused on release.
	r2.Release()
	r3, err := s.Open(7)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.InvalidateRepo(7)
	stale := r3.repo
	r3.Release()
	r4, err := s.Open(7)
	if err != nil {
		t.Fatalf("Open after second invalidate: %v", err)
	}
	if r4.repo == stale {
		t.Fatal("a handle acquired before an invalidation was re-cached after it")
	}
	r4.Release()
}

func TestRepoCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newRepoCache(2, 1)
	h := func() *gogit.Repository { return &gogit.Repository{} }

	dirs := []string{"a", "b", "c"}
	for _, d := range dirs {
		_, gen, ok := c.acquire(d)
		if ok {
			t.Fatalf("acquire(%s): unexpected warm handle in a fresh cache", d)
		}
		c.release(d, gen, h())
	}
	// "a" was least recently used and the cache holds 2 entries; it must be gone.
	if _, _, ok := c.acquire("a"); ok {
		t.Fatal("expected dir a to have been evicted")
	}
	if _, _, ok := c.acquire("c"); !ok {
		t.Fatal("expected dir c to retain its warm handle")
	}
}

func TestRepoCachePerRepoHandleBound(t *testing.T) {
	c := newRepoCache(4, 2)
	_, gen, _ := c.acquire("a")
	for i := 0; i < 5; i++ {
		c.release("a", gen, &gogit.Repository{})
	}
	hits := 0
	for i := 0; i < 5; i++ {
		if _, _, ok := c.acquire("a"); ok {
			hits++
		}
	}
	if hits != 2 {
		t.Fatalf("idle handles kept = %d, want the per-repo bound of 2", hits)
	}
}

func TestOverridePathsAreNeverCached(t *testing.T) {
	s := NewStore(t.TempDir())
	t.Cleanup(s.Close)
	// An override path is a user-supplied working tree (browse mode); it may
	// change outside our control, so its handles must stay uncached.
	dir := t.TempDir()
	if _, err := gogit.PlainInit(dir, false); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	s.RegisterPath(42, dir)

	r1, err := s.Open(42)
	if err != nil {
		t.Fatalf("Open override: %v", err)
	}
	if r1.cacheDir != "" {
		t.Fatal("override-path Repo must not participate in the handle cache")
	}
	h1 := r1.repo
	r1.Release()
	if r1.repo != h1 {
		t.Fatal("Release on an uncached Repo must be a no-op")
	}
}

func TestReleasedHandleStillReadsRepo(t *testing.T) {
	s := newStoreWithRepo(t, 9)
	// Cycle a handle through the cache several times and confirm reads work on
	// the reused handle (the cache must hand back a functional repository).
	for i := 0; i < 3; i++ {
		r, err := s.Open(9)
		if err != nil {
			t.Fatalf("Open round %d: %v", i, err)
		}
		if _, err := r.Branches(); err != nil {
			t.Fatalf("Branches on cycle %d: %v", i, err)
		}
		r.Release()
	}
}

func TestDirKeysAreStable(t *testing.T) {
	s := NewStore("/data")
	for _, pk := range []int64{1, 256, 257} {
		want := fmt.Sprintf("/data/%d/%d.git", pk%256, pk)
		if got := s.Dir(pk); got != want {
			t.Fatalf("Dir(%d) = %q, want %q", pk, got, want)
		}
	}
}
