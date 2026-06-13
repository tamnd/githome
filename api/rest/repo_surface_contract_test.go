package rest

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestRepositoriesGlobalList covers the global GET /repositories id-cursor
// listing the compat review flagged as missing (R01-25): public repositories
// come back ascending by id, per_page bounds the page, and a since cursor
// resumes after a given id.
func TestRepositoriesGlobalList(t *testing.T) {
	fx := repoServer(t)
	ctx := context.Background()

	// A second public repository so the page has more than one entry and the
	// cursor has somewhere to advance to.
	second := &store.RepoRow{OwnerPK: fx.ownerPK, Name: "world", DefaultBranch: "main"}
	if err := fx.st.InsertRepo(ctx, second); err != nil {
		t.Fatalf("insert second repo: %v", err)
	}
	// A private repository must not appear in the public listing.
	priv := &store.RepoRow{OwnerPK: fx.ownerPK, Name: "secret", Private: true, DefaultBranch: "main"}
	if err := fx.st.InsertRepo(ctx, priv); err != nil {
		t.Fatalf("insert private repo: %v", err)
	}

	resp, body := get(t, fx.srv, "/repositories")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list repositories: status %d, body %s", resp.StatusCode, body)
	}
	var repos []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
	}
	decodeBody(t, body, &repos)
	if len(repos) != 2 {
		t.Fatalf("want 2 public repos, got %d: %s", len(repos), body)
	}
	if repos[0].ID >= repos[1].ID {
		t.Errorf("repos not ascending by id: %d then %d", repos[0].ID, repos[1].ID)
	}
	for _, r := range repos {
		if r.Private || r.Name == "secret" {
			t.Errorf("private repo leaked into listing: %+v", r)
		}
		if r.FullName == "" {
			t.Errorf("repo missing full_name: %+v", r)
		}
	}

	// per_page=1 returns the first repo and a rel="next" Link carrying the
	// since cursor; following it returns the second.
	resp, body = get(t, fx.srv, "/repositories?per_page=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("paged list: status %d, body %s", resp.StatusCode, body)
	}
	var first []struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, body, &first)
	if len(first) != 1 {
		t.Fatalf("per_page=1 returned %d repos: %s", len(first), body)
	}
	if link := resp.Header.Get("Link"); !strings.Contains(link, `rel="next"`) || !strings.Contains(link, "since=") {
		t.Errorf("missing since next Link: %q", link)
	}

	// Resuming after the last id yields the rest, exclusive of the cursor.
	resp, body = get(t, fx.srv, "/repositories?since="+itoa(first[0].ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("since list: status %d, body %s", resp.StatusCode, body)
	}
	var rest []struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, body, &rest)
	for _, r := range rest {
		if r.ID <= first[0].ID {
			t.Errorf("since cursor not exclusive: got id %d <= %d", r.ID, first[0].ID)
		}
	}

	// A bad per_page is a validation error, not a silent clamp.
	if resp, _ := get(t, fx.srv, "/repositories?per_page=0"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("per_page=0: status %d, want 422", resp.StatusCode)
	}
}

// TestCommunityProfile covers GET /repos/{owner}/{repo}/community/profile
// (R01-25): the health percentage counts the present recommended files, the
// README and description on the octocat/hello fixture register, and absent
// files are null.
func TestCommunityProfile(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/community/profile", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("community profile: status %d, body %s", resp.StatusCode, body)
	}
	var prof struct {
		HealthPercentage int     `json:"health_percentage"`
		Description      *string `json:"description"`
		Files            struct {
			Readme *struct {
				HTMLURL string `json:"html_url"`
			} `json:"readme"`
			License      *struct{} `json:"license"`
			Contributing *struct{} `json:"contributing"`
		} `json:"files"`
		ContentReportsEnabled bool `json:"content_reports_enabled"`
	}
	decodeBody(t, body, &prof)

	if prof.Files.Readme == nil {
		t.Errorf("README present in fixture but reported absent: %s", body)
	} else if !strings.Contains(prof.Files.Readme.HTMLURL, "/blob/master/README.md") {
		t.Errorf("readme html_url = %q", prof.Files.Readme.HTMLURL)
	}
	if prof.Files.License != nil {
		t.Errorf("fixture has no LICENSE but one was reported: %s", body)
	}
	if prof.Description == nil || *prof.Description == "" {
		t.Errorf("description should be reported: %s", body)
	}
	// README + description present out of seven checks.
	if prof.HealthPercentage != 2*100/7 {
		t.Errorf("health_percentage = %d, want %d", prof.HealthPercentage, 2*100/7)
	}
	if prof.ContentReportsEnabled {
		t.Errorf("content_reports_enabled should be false")
	}
}

// TestCodeownersErrors covers GET /repos/{owner}/{repo}/codeowners/errors
// (R01-25): a repository with no CODEOWNERS reports no errors, and a file with a
// rule that names no owner and one with an invalid owner token are reported.
func TestCodeownersErrors(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/codeowners/errors", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("codeowners (none): status %d, body %s", resp.StatusCode, body)
	}
	var none struct {
		Errors []struct{} `json:"errors"`
	}
	decodeBody(t, body, &none)
	if len(none.Errors) != 0 {
		t.Errorf("no CODEOWNERS should yield no errors: %s", body)
	}

	// Commit a CODEOWNERS with a valid line, a line missing an owner, and a line
	// with an invalid owner token.
	content := "# owners\n*.go @octocat\ndocs/\n/api notanowner\n"
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	if resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/CODEOWNERS", fx.token,
		`{"message":"add codeowners","content":"`+b64+`"}`); resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("write CODEOWNERS: status %d, body %s", resp.StatusCode, body)
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/codeowners/errors", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("codeowners (errors): status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		Errors []struct {
			Line int    `json:"line"`
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"errors"`
	}
	decodeBody(t, body, &got)
	if len(got.Errors) != 2 {
		t.Fatalf("want 2 errors (missing owner, invalid owner), got %d: %s", len(got.Errors), body)
	}
	var missing, invalid bool
	for _, e := range got.Errors {
		if e.Path != "CODEOWNERS" {
			t.Errorf("error path = %q, want CODEOWNERS", e.Path)
		}
		switch e.Kind {
		case "Missing owner":
			missing = true
			if e.Line != 3 {
				t.Errorf("missing-owner line = %d, want 3", e.Line)
			}
		case "Invalid owner":
			invalid = true
			if e.Line != 4 {
				t.Errorf("invalid-owner line = %d, want 4", e.Line)
			}
		}
	}
	if !missing || !invalid {
		t.Errorf("expected both a missing-owner and invalid-owner error: %s", body)
	}
}
