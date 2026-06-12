package rest

import (
	"net/http"
	"strings"
	"testing"
)

// The reply flow has two wire shapes: the documented numbered path
// POST /pulls/{number}/comments/{comment_id}/replies, and the legacy
// in_reply_to field on the plain comment-create body. Both must thread the
// reply and render in_reply_to_id as the parent's public id.

// seedRootComment opens a review comment on the fixture pull and returns its id.
func seedRootComment(t *testing.T, fx reviewFixture) int64 {
	t.Helper()
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/comments", fx.reviewToken,
		`{"path":"feature.txt","line":1,"side":"RIGHT","body":"Root comment."}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed root comment status %d, body %s", resp.StatusCode, body)
	}
	return jsonInt(t, body, "id")
}

func TestReviewCommentReplyNumberedPathContract(t *testing.T) {
	fx := reviewServer(t)
	rootID := seedRootComment(t, fx)
	resp, body := authedSend(t, fx.srv, http.MethodPost,
		"/repos/octocat/hello/pulls/1/comments/"+itoa(rootID)+"/replies", fx.ownerToken, `{"body":"A reply."}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	if got := jsonInt(t, body, "in_reply_to_id"); got != rootID {
		t.Errorf("in_reply_to_id = %d, want parent id %d", got, rootID)
	}
	if !strings.Contains(string(body), `"path":"feature.txt"`) {
		t.Errorf("reply did not inherit the thread anchor: %s", body)
	}
}

func TestReviewCommentReplyNumberedPathWrongPull(t *testing.T) {
	fx := reviewServer(t)
	rootID := seedRootComment(t, fx)
	// The parent comment lives on pull 1; a different pull number finds nothing.
	resp, body := authedSend(t, fx.srv, http.MethodPost,
		"/repos/octocat/hello/pulls/999/comments/"+itoa(rootID)+"/replies", fx.ownerToken, `{"body":"A reply."}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404, body %s", resp.StatusCode, body)
	}
}

func TestReviewCommentLegacyInReplyToContract(t *testing.T) {
	fx := reviewServer(t)
	rootID := seedRootComment(t, fx)
	// The legacy create form carries only a body and the parent's id, no path.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/comments", fx.ownerToken,
		`{"body":"Legacy reply.","in_reply_to":`+itoa(rootID)+`}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	if got := jsonInt(t, body, "in_reply_to_id"); got != rootID {
		t.Errorf("in_reply_to_id = %d, want parent id %d", got, rootID)
	}
}

func TestReviewCommentCreateStillRequiresAnchor(t *testing.T) {
	fx := reviewServer(t)
	// Without in_reply_to, a comment with no path is still a validation error.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/comments", fx.ownerToken,
		`{"body":"No anchor."}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"missing_field"`) {
		t.Errorf("missing_field error absent: %s", body)
	}
}
