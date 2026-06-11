package rest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// commitsList GETs the commits endpoint with the given query string and
// returns the decoded array.
func commitsList(t *testing.T, fx repoFixture, query string) []map[string]any {
	t.Helper()
	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/commits"+query, "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("commits%s status %d, body %s", query, resp.StatusCode, body)
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCommitsAuthorCommitter covers the author and committer filters: an
// email match, a name match, case-insensitivity, and a miss.
func TestCommitsAuthorCommitter(t *testing.T) {
	fx := repoServer(t)

	if got := commitsList(t, fx, "?author=octo@example.com"); len(got) != 2 {
		t.Fatalf("author email filter returned %d commits, want 2", len(got))
	}
	if got := commitsList(t, fx, "?author=octo+cat"); len(got) != 2 {
		t.Fatalf("author name filter returned %d commits, want 2", len(got))
	}
	if got := commitsList(t, fx, "?author=nobody@example.com"); len(got) != 0 {
		t.Fatalf("author miss returned %d commits, want 0", len(got))
	}
	if got := commitsList(t, fx, "?committer=Octo"); len(got) != 2 {
		t.Fatalf("committer filter returned %d commits, want 2", len(got))
	}
	if got := commitsList(t, fx, "?committer=stranger"); len(got) != 0 {
		t.Fatalf("committer miss returned %d commits, want 0", len(got))
	}
}

// TestCommitsSinceUntil covers the time window. The fixture's two commits are
// pinned at 2026-01-02, and a third lands at request time, so a boundary
// between them splits the history cleanly.
func TestCommitsSinceUntil(t *testing.T) {
	fx := repoServer(t)
	b64 := base64.StdEncoding.EncodeToString([]byte("late entry\n"))
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/late.txt", fx.token,
		`{"message":"a later commit","content":"`+b64+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("late commit status %d, body %s", resp.StatusCode, body)
	}

	if got := commitsList(t, fx, ""); len(got) != 3 {
		t.Fatalf("unfiltered returned %d commits, want 3", len(got))
	}
	if got := commitsList(t, fx, "?since=2026-02-01T00:00:00Z"); len(got) != 1 {
		t.Fatalf("since returned %d commits, want 1", len(got))
	}
	if got := commitsList(t, fx, "?until=2026-02-01T00:00:00Z"); len(got) != 2 {
		t.Fatalf("until returned %d commits, want 2", len(got))
	}
	if got := commitsList(t, fx, "?since=2026-01-01T00:00:00Z&until=2026-02-01T00:00:00Z"); len(got) != 2 {
		t.Fatalf("since+until returned %d commits, want 2", len(got))
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/commits?since=yesterday", "token "+fx.token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad since status %d, want 422, body %s", resp.StatusCode, body)
	}
	if fe := firstFieldError(t, body); fe["field"] != "since" {
		t.Fatalf("bad since field error = %v", fe)
	}
}

// TestCommitsPagingLink covers the page window and the uncounted rel="next"
// Link header the walk emits when one more commit exists past the page.
func TestCommitsPagingLink(t *testing.T) {
	fx := repoServer(t)

	req, err := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/commits?per_page=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "token "+fx.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var page []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0]["sha"] != fx.headSHA {
		t.Fatalf("page 1 = %v", page)
	}
	link := resp.Header.Get("Link")
	if !strings.Contains(link, `rel="next"`) {
		t.Fatalf("page 1 Link = %q, want rel=next", link)
	}

	if got := commitsList(t, fx, "?per_page=1&page=2"); len(got) != 1 || got[0]["sha"] != fx.firstSHA {
		t.Fatalf("page 2 = %v", got)
	}
}
