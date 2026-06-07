package rest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/gittransport"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/realworld"
	"github.com/tamnd/githome/store"
)

// TestLinuxOperators exercises every operation class the workload model states
// SLOs against (realworld.OpClass: X-cond, R-meta, R-git, T-git, W-meta) over a
// real torvalds/linux repository, served by the same REST surface and git
// transport the binary mounts. linux is the corpus's transport- and git-read-
// dominated repository (its spec mix is T-git 65, R-git 25, X-cond 10, and zero
// metadata), so it is the right repository to prove the git transport and git
// read paths hold on a real kernel-sized object graph rather than a fixture.
//
// It needs a real linux repository on disk; point GITHOME_SCALE_GITREPO at a
// bare or working clone (a full `git clone --bare https://github.com/torvalds/linux`
// is what it is meant for) and run:
//
//	GITHOME_SCALE_GITREPO=/path/to/linux.git \
//	  go test ./api/rest -run TestLinuxOperators -v -timeout 30m
//
// It is skipped when the variable is unset, so the ordinary suite never pays the
// clone. The served repository is filled by a real local bare clone, which
// hardlinks the object database, so standing it up stays cheap even at 6GB.
func TestLinuxOperators(t *testing.T) {
	src := os.Getenv("GITHOME_SCALE_GITREPO")
	if src == "" {
		t.Skip("set GITHOME_SCALE_GITREPO=<path to a real linux repo> to run the all-operators benchmark")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary not on PATH: %v", err)
	}
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	owner := &store.UserRow{Login: "torvalds", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &owner.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "linux", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	// Fill the served path with a real local bare clone of the kernel.
	gitStore := git.NewStore(t.TempDir())
	dst := gitStore.Dir(repo.PK)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}
	cloneStart := time.Now()
	gitExec(t, "", "clone", "--bare", "--local", src, dst)
	t.Logf("served linux: local bare clone in %s", time.Since(cloneStart).Round(time.Millisecond))

	// Resolve the real ids the read operators address, plus a real root file.
	gr, err := gitStore.Open(repo.PK)
	if err != nil {
		t.Fatalf("open served repo: %v", err)
	}
	head, err := gr.HEAD()
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	commit, err := gr.Commit(head.Commit)
	if err != nil {
		t.Fatalf("Commit(HEAD): %v", err)
	}
	const realFile = "MAINTAINERS" // a large file present at the kernel root for decades
	path, err := gr.PathAt(head.Commit, realFile)
	if err != nil || path.File == nil {
		t.Fatalf("resolve %s at HEAD: %v", realFile, err)
	}
	t.Logf("served linux HEAD %s, %s is %d bytes", head.Commit, realFile, path.Entry.Size)

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	repoSvc := domain.NewRepoService(st, gitStore)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     domain.NewIssueService(st, repoSvc),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	gittransport.Mount(root, &gittransport.Service{Repos: repoSvc, Git: gitStore, Auth: authSvc})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	// Each operator class, timed, against the real kernel. results keeps the row
	// per class so the summary prints what ran and how it fared.
	type opResult struct {
		class realworld.OpClass
		op    string
		dur   time.Duration
		note  string
	}
	var results []opResult
	record := func(class realworld.OpClass, op string, note string, fn func() error) {
		t.Helper()
		start := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("%s (%s): %v", op, class, err)
		}
		results = append(results, opResult{class, op, time.Since(start), note})
	}

	// T-git: the transport. A real git clone of the real kernel over Smart HTTP,
	// served by githome. depth=1 keeps the transfer to one tree's worth of objects
	// so the test stays tractable while still driving advertise + upload-pack end
	// to end; ls-remote drives the ref advertisement on its own.
	record(realworld.OpTGit, "ls-remote (advertise)", "1 round trip", func() error {
		out := gitExec(t, "", "ls-remote", srv.URL+"/torvalds/linux.git", "HEAD")
		if !strings.Contains(out, head.Commit) {
			return fmt.Errorf("advertised HEAD %q missing %s", out, head.Commit)
		}
		return nil
	})
	cloneDir := filepath.Join(t.TempDir(), "via-githome")
	record(realworld.OpTGit, "git clone (upload-pack)", "depth=1", func() error {
		gitExec(t, "", "clone", "--quiet", "--depth=1", srv.URL+"/torvalds/linux.git", cloneDir)
		gitExec(t, cloneDir, "fsck", "--full", "--strict")
		if got := gitExec(t, cloneDir, "rev-parse", "HEAD"); got != head.Commit {
			return fmt.Errorf("cloned HEAD %s, want %s", got, head.Commit)
		}
		return nil
	})

	// R-git: git reads over the REST API on the real object graph.
	record(realworld.OpRGit, "GET git/trees?recursive=1", "HEAD tree", func() error {
		resp, body := authedGet(t, srv, "/repos/torvalds/linux/git/trees/"+commit.Tree+"?recursive=1", "token "+g.Plaintext)
		return want200(resp, body)
	})
	record(realworld.OpRGit, "GET contents/"+realFile, "real file", func() error {
		resp, body := authedGet(t, srv, "/repos/torvalds/linux/contents/"+realFile, "token "+g.Plaintext)
		return want200(resp, body)
	})
	record(realworld.OpRGit, "GET git/blobs/{sha}", "real blob", func() error {
		resp, body := authedGet(t, srv, "/repos/torvalds/linux/git/blobs/"+path.Entry.SHA, "token "+g.Plaintext)
		return want200(resp, body)
	})

	// X-cond: a conditional read. Fetch the repo, then re-fetch with the returned
	// validator and require the 304 the poll flood depends on.
	record(realworld.OpXCond, "GET repo If-None-Match", "expect 304", func() error {
		resp, body := authedGet(t, srv, "/repos/torvalds/linux", "token "+g.Plaintext)
		if err := want200(resp, body); err != nil {
			return err
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			return fmt.Errorf("no ETag on repo read, cannot exercise the conditional path")
		}
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/repos/torvalds/linux", nil)
		req.Header.Set("Authorization", "token "+g.Plaintext)
		req.Header.Set("If-None-Match", etag)
		r2, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = r2.Body.Close() }()
		if r2.StatusCode != http.StatusNotModified {
			return fmt.Errorf("conditional GET status %d, want 304", r2.StatusCode)
		}
		return nil
	})

	// R-meta and W-meta carry zero weight in linux's spec mix, but the suite still
	// proves both classes work against this repo so "all operators" is honest: the
	// write opens an issue, the read fetches it back.
	record(realworld.OpWMeta, "POST issues", "open issue", func() error {
		resp, body := authedSend(t, srv, http.MethodPost, "/repos/torvalds/linux/issues", g.Plaintext,
			`{"title":"boot regression on real hardware"}`)
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("create issue status %d: %s", resp.StatusCode, body)
		}
		return nil
	})
	record(realworld.OpRMeta, "GET issues/1", "read issue", func() error {
		resp, body := authedGet(t, srv, "/repos/torvalds/linux/issues/1", "token "+g.Plaintext)
		return want200(resp, body)
	})

	mix := realworld.MixFor("torvalds/linux")
	t.Logf("operator coverage on real torvalds/linux (spec mix in parens):")
	t.Logf("  %-7s %-26s %-12s %10s", "class", "operation", "detail", "latency")
	for _, r := range results {
		t.Logf("  %-7s %-26s %-12s %10s   (mix %d%%)",
			r.class, r.op, r.note, r.dur.Round(time.Microsecond), mix[r.class])
	}
}

// want200 turns a non-200 REST response into an error carrying the body.
func want200(resp *http.Response, body []byte) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return nil
}
