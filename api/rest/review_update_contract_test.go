package rest

import (
	"net/http"
	"strings"
	"testing"
)

// PUT /pulls/{number}/reviews/{review_id} replaces a review's summary body, and
// GET .../reviews/{review_id}/comments lists the inline comments one review
// carries. Together with dismissals these are the octokit review-management calls.

func TestReviewUpdateContract(t *testing.T) {
	fx := reviewServer(t)
	_, rev := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"APPROVE","body":"First pass."}`)
	id := jsonInt(t, rev, "id")
	resp, body := authedSend(t, fx.srv, http.MethodPut,
		"/repos/octocat/hello/pulls/1/reviews/"+itoa(id), fx.reviewToken, `{"body":"Second thoughts."}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"body":"Second thoughts."`) {
		t.Errorf("updated body missing: %s", body)
	}
	if !strings.Contains(string(body), `"state":"APPROVED"`) {
		t.Errorf("state changed by a body edit: %s", body)
	}
}

func TestReviewUpdateAuthorOnly(t *testing.T) {
	fx := reviewServer(t)
	_, rev := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"APPROVE","body":"First pass."}`)
	id := jsonInt(t, rev, "id")
	// The owner has write access but did not author the review.
	resp, body := authedSend(t, fx.srv, http.MethodPut,
		"/repos/octocat/hello/pulls/1/reviews/"+itoa(id), fx.ownerToken, `{"body":"Rewritten."}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403, body %s", resp.StatusCode, body)
	}
}

func TestReviewUpdateRequiresBody(t *testing.T) {
	fx := reviewServer(t)
	_, rev := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"APPROVE","body":"First pass."}`)
	id := jsonInt(t, rev, "id")
	resp, body := authedSend(t, fx.srv, http.MethodPut,
		"/repos/octocat/hello/pulls/1/reviews/"+itoa(id), fx.reviewToken, `{}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"missing_field"`) {
		t.Errorf("missing_field error absent: %s", body)
	}
}

func TestReviewCommentsListContract(t *testing.T) {
	fx := reviewServer(t)
	_, rev := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"COMMENT","body":"Inline notes.","comments":[{"path":"feature.txt","line":1,"side":"RIGHT","body":"Inline one."}]}`)
	id := jsonInt(t, rev, "id")
	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls/1/reviews/"+itoa(id)+"/comments")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"body":"Inline one."`) {
		t.Errorf("review comment missing: %s", body)
	}
	if !strings.Contains(string(body), `"pull_request_review_id":`+itoa(id)) {
		t.Errorf("pull_request_review_id missing: %s", body)
	}
}

func TestReviewCommentsListUnknownReview(t *testing.T) {
	fx := reviewServer(t)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls/1/reviews/999999/comments")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404, body %s", resp.StatusCode, body)
	}
}
