package repo

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamnd/githome/domain"
)

// TestCommitsAuthorFilter narrows the history by the author's name or email and
// expects the unmatched filter to render the blankslate, never an error.
func TestCommitsAuthorFilter(t *testing.T) {
	fx := newFixture(t)

	resp, body := get(t, fx.srv, "/octocat/hello/commits?author=octo%40example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "add the guide") || !strings.Contains(body, "initial commit") {
		t.Errorf("author-matched history lost its commits:\n%s", body)
	}

	resp, body = get(t, fx.srv, "/octocat/hello/commits?author=nobody")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "No commits to show.") {
		t.Errorf("unmatched author filter did not render the blankslate:\n%s", body)
	}
}

// TestCommitsPathQueryFilter covers ?path= on the bare /commits URL: only the
// commits touching the path render, the same narrowing the URL-tail path form
// applies.
func TestCommitsPathQueryFilter(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/commits?path=docs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "add the guide") {
		t.Errorf("path filter lost the docs commit:\n%s", body)
	}
	if strings.Contains(body, "initial commit") {
		t.Errorf("path filter leaked a commit that does not touch docs:\n%s", body)
	}
}

// TestCommitsSinceUntilFilter bounds the walk by commit time. The fixture's
// commits sit on Jan 2, 2026: an until before that day empties the page, a
// bare until on the day itself reads inclusively, and a since on the day keeps
// both commits.
func TestCommitsSinceUntilFilter(t *testing.T) {
	fx := newFixture(t)

	_, body := get(t, fx.srv, "/octocat/hello/commits?until=2026-01-01")
	if !strings.Contains(body, "No commits to show.") {
		t.Errorf("until before the history did not empty the page:\n%s", body)
	}

	_, body = get(t, fx.srv, "/octocat/hello/commits?until=2026-01-02")
	if !strings.Contains(body, "initial commit") {
		t.Errorf("bare until on the commit day must read inclusively:\n%s", body)
	}

	_, body = get(t, fx.srv, "/octocat/hello/commits?since=2026-01-02")
	if !strings.Contains(body, "add the guide") {
		t.Errorf("since on the commit day lost the history:\n%s", body)
	}

	// An unparseable date is dropped, not an error.
	resp, _ := get(t, fx.srv, "/octocat/hello/commits?since=banana")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("invalid since: status %d, want 200", resp.StatusCode)
	}
}

// TestCommitsPagination grows the history past one page and walks it with
// ?page=: page one shows the newest thirty with only the Older hop, the last
// page carries the oldest commit with only the Newer hop, and the hops keep
// the filters.
func TestCommitsPagination(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	repo, err := fx.repos.GetRepo(ctx, 0, fx.owner, fx.repo)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	// 31 more commits on top of the fixture's 2: 33 total, two pages.
	for i := range 31 {
		_, err := fx.repos.WriteFile(repo, domain.WriteFileInput{
			Path:        "counter.txt",
			Content:     []byte(strconv.Itoa(i) + "\n"),
			Message:     "count " + strconv.Itoa(i),
			AuthorName:  "Octo Cat",
			AuthorEmail: "octo@example.com",
			Branch:      "master",
		})
		if err != nil {
			t.Fatalf("WriteFile %d: %v", i, err)
		}
	}

	resp, body := get(t, fx.srv, "/octocat/hello/commits")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "count 30") {
		t.Error("page one lost the newest commit")
	}
	if strings.Contains(body, "initial commit") {
		t.Error("page one leaked the oldest commit past the page bound")
	}
	if !strings.Contains(body, `href="/octocat/hello/commits/master?page=2"`) {
		t.Errorf("page one is missing the Older hop:\n%s", body)
	}
	if strings.Contains(body, `rel="prev"`) {
		t.Error("page one must not link a Newer hop")
	}

	resp, body = get(t, fx.srv, "/octocat/hello/commits?page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 2 status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "initial commit") {
		t.Error("page two lost the oldest commit")
	}
	if !strings.Contains(body, `href="/octocat/hello/commits/master"`) || !strings.Contains(body, `rel="prev"`) {
		t.Errorf("page two is missing the Newer hop back to the canonical URL:\n%s", body)
	}
	if strings.Contains(body, "?page=3") {
		t.Error("page two invented an Older hop past the history")
	}

	// A filter survives the pagination hop.
	_, body = get(t, fx.srv, "/octocat/hello/commits?author=octo&page=2")
	if !strings.Contains(body, "author=octo") {
		t.Errorf("pagination dropped the author filter:\n%s", body)
	}
}
