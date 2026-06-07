package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/githome/git"
)

// TestScaleGitRead drives the git read layer against a real, large repository
// rather than a hand-built fixture, so the tree walk, blob materialization, and
// history walk are measured at a scale the small contract fixtures never reach
// (a full torvalds/linux carries about 1.45M commits and a HEAD tree just
// under 100k files). The test takes a repository already on disk and makes a
// real local bare clone of it into the store's own on-disk layout, so it works
// the same whether the source was cloned from the network or is a local mirror.
// Point it at one:
//
//	GITHOME_SCALE_GITREPO=/path/to/linux.git \
//	  go test ./git -run TestScaleGitRead -v -timeout 20m
//
// It is skipped when the variable is unset, so an ordinary `go test ./...`
// never pays the clone.
func TestScaleGitRead(t *testing.T) {
	src := os.Getenv("GITHOME_SCALE_GITREPO")
	if src == "" {
		t.Skip("set GITHOME_SCALE_GITREPO=<path to a real repo> to run the real-repo git read benchmark")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary not on PATH: %v", err)
	}

	// A real clone, just from a local source instead of the network. --bare
	// matches how the server stores repositories and --local hardlinks the object
	// database, so cloning an 800MB history stays cheap. The destination is the
	// exact path the store resolves for this repository's pk.
	const pk = 1
	root := t.TempDir()
	store := git.NewStore(root)
	dst := store.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}
	cloneStart := time.Now()
	out, err := exec.Command("git", "clone", "--bare", "--local", src, dst).CombinedOutput()
	if err != nil {
		t.Fatalf("local bare clone of %s: %v\n%s", src, err, out)
	}
	t.Logf("local bare clone in %s -> %s", time.Since(cloneStart).Round(time.Millisecond), dst)

	repo, err := store.Open(pk)
	if err != nil {
		t.Fatalf("open cloned repo: %v", err)
	}

	timed := func(label string, fn func() error) {
		t.Helper()
		start := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		t.Logf("  %-26s %s", label, time.Since(start).Round(time.Microsecond))
	}

	head, err := repo.HEAD()
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	t.Logf("HEAD %s at %s", head.Name, head.Commit)

	t.Logf("read-path latency on a real repository:")

	timed("Commit(HEAD)", func() error {
		_, err := repo.Commit(head.Commit)
		return err
	})

	// The recursive HEAD tree is the heavy read the Contents and Git Trees APIs
	// serve; on a real repo it is thousands of entries.
	var treeEntries int
	timed("Tree(HEAD, recursive)", func() error {
		tr, err := repo.Tree(head.Commit, true)
		if err != nil {
			return err
		}
		treeEntries = len(tr.Entries)
		return nil
	})
	t.Logf("  HEAD tree entries: %d", treeEntries)

	// Walk history in pages from the tip, the git log the commits API pages. A
	// deep cap exercises the walk far past the first screen. A repository cloned
	// shallow (some local mirrors are) truncates history at a boundary where the
	// next parent is genuinely absent; the walk reports that as a missing object,
	// which is a property of the source, not a read bug, so the loop logs it and
	// stops rather than failing the whole benchmark.
	for _, max := range []int{30, 1000, 20000} {
		start := time.Now()
		commits, err := repo.Log(git.LogOpts{From: head.Commit, Max: max})
		if err != nil && strings.Contains(err.Error(), "object not found") {
			t.Logf("  Log(HEAD, max=%d) stopped at a shallow boundary (source is a shallow clone)", max)
			break
		}
		if err != nil {
			t.Fatalf("Log max=%d: %v", max, err)
		}
		if len(commits) == 0 {
			t.Fatalf("Log max=%d returned no commits", max)
		}
		t.Logf("  %-26s %s (%d commits)", "Log(HEAD, max="+itoa(max)+")",
			time.Since(start).Round(time.Microsecond), len(commits))
	}

	// Find the largest blob in the HEAD tree and read it, the worst case for blob
	// materialization, then prove the size guard trips on it.
	tr, err := repo.Tree(head.Commit, true)
	if err != nil {
		t.Fatalf("tree for blob pick: %v", err)
	}
	var biggest git.TreeEntry
	for _, e := range tr.Entries {
		if e.Type == git.ObjectBlob && e.Size > biggest.Size {
			biggest = e
		}
	}
	if biggest.SHA == "" {
		t.Fatal("no blob found in HEAD tree")
	}
	t.Logf("largest HEAD blob: %s (%d bytes)", biggest.Path, biggest.Size)

	timed("Blob(largest)", func() error {
		b, err := repo.Blob(biggest.SHA)
		if err != nil {
			return err
		}
		if b.Size != biggest.Size {
			t.Fatalf("blob size %d, want %d", b.Size, biggest.Size)
		}
		return nil
	})

	// Cap below the largest blob and confirm the guard refuses it from the size
	// in the header, then restore the default and confirm it serves again.
	store.SetMaxBlobBytes(biggest.Size - 1)
	capped, err := store.Open(pk)
	if err != nil {
		t.Fatalf("reopen with cap: %v", err)
	}
	if _, err := capped.Blob(biggest.SHA); err != git.ErrBlobTooLarge {
		t.Fatalf("capped blob read error = %v, want ErrBlobTooLarge", err)
	}
	store.SetMaxBlobBytes(-1)
	uncapped, err := store.Open(pk)
	if err != nil {
		t.Fatalf("reopen uncapped: %v", err)
	}
	if _, err := uncapped.Blob(biggest.SHA); err != nil {
		t.Fatalf("uncapped blob read after disabling cap: %v", err)
	}
}

// itoa keeps the label building free of an fmt import in the hot helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
