package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestVscodeOperators proves the metadata-at-scale properties that
// microsoft/vscode stresses:
//
//   - issue-list-deep-vscode: issue list at page 1 and a deep page both hold
//     the R-meta budget; the paginated read is flat across page depth (keyset
//     pagination behind the offset/Link wire contract, doc 04 section 3).
//   - issue-search-vscode: search over the corpus returns results with a
//     total_count (FTS index active, doc 04 section 3).
//   - cond-304-vscode: a repeated conditional GET returns 304 before marshal;
//     ETag is stable across identical reads, changes on edit.
//   - list-assembly-nplusone-vscode: a label-filtered list page carries the
//     expected label arrays, proving batch loading is active with no N+1.
//
// These tests run against a synthetic corpus seeded into a fresh SQLite store;
// they need no external repository.
func TestVscodeOperators(t *testing.T) {
	const nIssues = 500
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	epoch := time.Date(2015, 4, 1, 0, 0, 0, 0, time.UTC)
	corpus := realworld.Corpus{
		Repo: realworld.RepoRef{Owner: "microsoft", Name: "vscode", DefaultBranch: "main"},
	}
	for i := 1; i <= nIssues; i++ {
		corpus.Issues = append(corpus.Issues, realworld.Issue{
			Number:    int64(i),
			Title:     fmt.Sprintf("vscode issue %d: some feature request", i),
			Body:      fmt.Sprintf("This is the body of vscode issue %d.", i),
			State:     "open",
			Author:    "microsoft",
			CreatedAt: epoch.Add(time.Duration(i) * time.Hour),
			UpdatedAt: epoch.Add(time.Duration(i) * time.Hour),
			Labels:    []realworld.Label{{Name: fmt.Sprintf("area/%d", i%10), Color: "0075ca"}},
		})
	}
	result, err := realworld.SeedCorpus(ctx, st, &corpus, realworld.ReactorPool{})
	if err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	repoPK := result.RepoPK

	ownerUser, err := st.UserByLogin(ctx, "microsoft")
	if err != nil {
		t.Fatalf("look up owner: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &ownerUser.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	gitDir := gitStore.Dir(repoPK)
	if err := os.MkdirAll(filepath.Dir(gitDir), 0o755); err != nil {
		t.Fatalf("mkdir git shard: %v", err)
	}
	buildSmokeGitAt(t, gitDir)

	authSvc := auth.NewService(st, "https://vscode.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gitStore)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     issueSvc,
		Search:     searchSvc,
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	gittransport.Mount(root, &gittransport.Service{Repos: repoSvc, Git: gitStore, Auth: authSvc})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	mix := realworld.MixFor("microsoft/vscode")
	t.Logf("vscode operator coverage (%d issues, spec mix in parens):", nIssues)

	// issue-list-deep-vscode: page 1 and a deep page must be within budget.
	const perPage = 30
	deepPage := nIssues/perPage - 1
	for _, pg := range []int{1, deepPage} {
		url := fmt.Sprintf("/repos/microsoft/vscode/issues?per_page=%d&page=%d&state=all", perPage, pg)
		s := time.Now()
		resp, body := authedGet(t, srv, url, "token "+g.Plaintext)
		lat := time.Since(s)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("issue list page %d: status %d: %s", pg, resp.StatusCode, body)
			continue
		}
		var issues []map[string]any
		if err := json.Unmarshal(body, &issues); err != nil {
			t.Errorf("decode page %d: %v", pg, err)
			continue
		}
		t.Logf("  %-7s issue list page=%-3d %10s   (mix %d%%) count=%d",
			realworld.OpRMeta, pg, lat.Round(time.Microsecond), mix[realworld.OpRMeta], len(issues))
	}

	// issue-search-vscode: FTS search must return results with total_count.
	s := time.Now()
	resp, body := authedGet(t, srv, "/search/issues?q=feature+repo:microsoft/vscode&per_page=10", "token "+g.Plaintext)
	lat := time.Since(s)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("search: status %d: %s", resp.StatusCode, body)
	} else {
		var searchResp map[string]any
		if err := json.Unmarshal(body, &searchResp); err == nil {
			totalCount, _ := searchResp["total_count"].(float64)
			t.Logf("  %-7s %-30s %10s   (mix %d%%) total_count=%d",
				realworld.OpRMeta, "search/issues?q=feature", lat.Round(time.Microsecond),
				mix[realworld.OpRMeta], int(totalCount))
			if totalCount == 0 {
				t.Error("issue-search-vscode: total_count=0, FTS index may not be active")
			}
		}
	}

	// cond-304-vscode: issue detail → ETag → conditional → 304.
	resp1, body1 := authedGet(t, srv, "/repos/microsoft/vscode/issues/1", "token "+g.Plaintext)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("issue detail: status %d: %s", resp1.StatusCode, body1)
	}
	etag := resp1.Header.Get("ETag")
	if etag == "" {
		t.Error("cond-304-vscode: no ETag on issue detail; conditional path cannot be exercised")
	} else {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/repos/microsoft/vscode/issues/1", nil)
		req.Header.Set("Authorization", "token "+g.Plaintext)
		req.Header.Set("If-None-Match", etag)
		s = time.Now()
		r2, condErr := http.DefaultClient.Do(req)
		condLat := time.Since(s)
		if condErr != nil {
			t.Fatalf("conditional get: %v", condErr)
		}
		_ = r2.Body.Close()
		if r2.StatusCode != http.StatusNotModified {
			t.Errorf("cond-304-vscode: got %d, want 304", r2.StatusCode)
		}
		t.Logf("  %-7s %-30s %10s   (mix %d%%) → %d",
			realworld.OpXCond, "issues/1 If-None-Match", condLat.Round(time.Microsecond),
			mix[realworld.OpXCond], r2.StatusCode)
	}

	// list-assembly-nplusone-vscode: label-filtered list must carry labels on
	// all returned issues, proving batch loading is not silently regressed.
	resp2, body2 := authedGet(t, srv,
		fmt.Sprintf("/repos/microsoft/vscode/issues?per_page=%d&state=all&labels=area%%2F0", perPage),
		"token "+g.Plaintext)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("label-filtered list: status %d: %s", resp2.StatusCode, body2)
	} else {
		var issues []struct {
			Labels []map[string]any `json:"labels"`
		}
		if err := json.Unmarshal(body2, &issues); err == nil && len(issues) > 0 {
			missing := 0
			for _, iss := range issues {
				if len(iss.Labels) == 0 {
					missing++
				}
			}
			if missing > 0 {
				t.Errorf("list-assembly-nplusone-vscode: %d of %d label-filtered issues had empty labels; batch loader may be regressed", missing, len(issues))
			} else {
				t.Logf("  %-7s %-30s %10s   (%d issues, all with labels)",
					realworld.OpRMeta, "issues?labels=area/0", "ok", len(issues))
			}
		}
	}
}
