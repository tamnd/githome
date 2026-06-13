package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestIssueEventsList confirms the events endpoint returns the issue's recorded
// action events rather than the old empty-array stub: closing an issue records a
// "closed" event the endpoint then surfaces with an actor and a stable URL.
func TestIssueEventsList(t *testing.T) {
	fx := issueServer(t)
	seedIssue(t, fx, `{"title":"Close me"}`)
	if resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.token,
		`{"state":"closed"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("close status %d, body %s", resp.StatusCode, body)
	}

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/issues/1/events", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status %d, body %s", resp.StatusCode, body)
	}
	var events []struct {
		ID    int64  `json:"id"`
		Event string `json:"event"`
		Actor *struct {
			Login string `json:"login"`
		} `json:"actor"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("decode events: %v\n%s", err, body)
	}
	if len(events) != 1 {
		t.Fatalf("want one event, got %d:\n%s", len(events), body)
	}
	if events[0].Event != "closed" {
		t.Errorf("event = %q, want closed", events[0].Event)
	}
	if events[0].Actor == nil || events[0].Actor.Login != "octocat" {
		t.Errorf("event actor = %+v, want octocat", events[0].Actor)
	}
	if !strings.Contains(events[0].URL, "/issues/events/") {
		t.Errorf("event url = %q, want an issues/events URL", events[0].URL)
	}
}

// TestIssueTimeline confirms the timeline merges comments and events in time
// order: a comment then a close yields a commented entry followed by a closed
// entry, replacing the former empty-array stub.
func TestIssueTimeline(t *testing.T) {
	fx := issueServer(t)
	seedIssue(t, fx, `{"title":"Discuss then close"}`)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/comments", fx.token,
		`{"body":"first thought"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("comment status %d, body %s", resp.StatusCode, body)
	}
	if resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.token,
		`{"state":"closed"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("close status %d, body %s", resp.StatusCode, body)
	}

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/issues/1/timeline", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timeline status %d, body %s", resp.StatusCode, body)
	}
	var entries []struct {
		Event string `json:"event"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("decode timeline: %v\n%s", err, body)
	}
	if len(entries) != 2 {
		t.Fatalf("want two timeline entries, got %d:\n%s", len(entries), body)
	}
	if entries[0].Event != "commented" || entries[0].Body != "first thought" {
		t.Errorf("first entry = %+v, want commented with body", entries[0])
	}
	if entries[1].Event != "closed" {
		t.Errorf("second entry event = %q, want closed", entries[1].Event)
	}
}

// TestAssigneeCheck confirms GET /assignees/{username} answers 204 for an
// assignable user (the owner) and 404 otherwise, matching GitHub's membership
// probe used before assigning.
func TestAssigneeCheck(t *testing.T) {
	fx := issueServer(t)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/assignees/octocat", "token "+fx.token)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("owner assignee check %d, want 204, body %s", resp.StatusCode, body)
	}
	resp, _ = authedGet(t, fx.srv, "/repos/octocat/hello/assignees/ghost", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("non-assignee check %d, want 404", resp.StatusCode)
	}
}

// TestMilestonesSortPagination confirms the list orders by due date ascending by
// default and pages with a Link header: with one milestone due in 2025 and one
// in 2030, asc puts 2025 first, and per_page=1 carries a rel="next" link.
func TestMilestonesSortPagination(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/milestones", fx.token,
		`{"title":"later","due_on":"2030-01-01T00:00:00Z"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("milestone later status %d, body %s", resp.StatusCode, body)
	}
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/milestones", fx.token,
		`{"title":"sooner","due_on":"2025-01-01T00:00:00Z"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("milestone sooner status %d, body %s", resp.StatusCode, body)
	}

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/milestones?sort=due_on&direction=asc", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("milestones status %d, body %s", resp.StatusCode, body)
	}
	var ms []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(body, &ms); err != nil {
		t.Fatalf("decode milestones: %v\n%s", err, body)
	}
	if len(ms) != 2 || ms[0].Title != "sooner" || ms[1].Title != "later" {
		t.Fatalf("due_on asc order wrong: %+v", ms)
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/milestones?per_page=1", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("paged milestones status %d, body %s", resp.StatusCode, body)
	}
	var first []json.RawMessage
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("decode paged: %v", err)
	}
	if len(first) != 1 {
		t.Errorf("per_page=1 returned %d milestones, want 1", len(first))
	}
	if link := resp.Header.Get("Link"); !strings.Contains(link, `rel="next"`) {
		t.Errorf("per_page=1 Link = %q, want a rel=\"next\"", link)
	}
}

// TestIssueCommentsSince confirms the comment list honors the since filter: a
// timestamp before the comment keeps it, one after drops it.
func TestIssueCommentsSince(t *testing.T) {
	fx := issueServer(t)
	seedIssue(t, fx, `{"title":"Discuss"}`)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/comments", fx.token,
		`{"body":"hello"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("comment status %d, body %s", resp.StatusCode, body)
	}

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/issues/1/comments?since=2000-01-01T00:00:00Z", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("since-past status %d, body %s", resp.StatusCode, body)
	}
	var past []json.RawMessage
	if err := json.Unmarshal(body, &past); err != nil {
		t.Fatalf("decode since-past: %v", err)
	}
	if len(past) != 1 {
		t.Errorf("since=2000 returned %d comments, want 1", len(past))
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/issues/1/comments?since=2999-01-01T00:00:00Z", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("since-future status %d, body %s", resp.StatusCode, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("since=2999 should drop the comment, got:\n%s", body)
	}
}

// seedIssue posts an issue and fails the test if the create does not 201.
func seedIssue(t *testing.T, fx issueFixture, body string) {
	t.Helper()
	if resp, b := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token, body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, b)
	}
}
