package rest

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// The update-branch tests reuse the review fixture: a feature->main pull where
// the owner holds write access. Advancing main through the contents API gives
// the base branch the new commit update-branch merges down.

// advanceMain commits a new file to main as the owner, putting the base branch
// ahead of the pull request's head.
func (fx reviewFixture) advanceMain(t *testing.T, path string) {
	t.Helper()
	content := base64.StdEncoding.EncodeToString([]byte("base moved\n"))
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/"+path, fx.ownerToken,
		`{"message":"advance main","content":"`+content+`","branch":"main"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("advance main status %d, body %s", resp.StatusCode, body)
	}
}

func TestPullUpdateBranchContract(t *testing.T) {
	fx := reviewServer(t)
	fx.advanceMain(t, "base.txt")
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/update-branch", fx.ownerToken, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d, want 202, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"message":"Updating pull request branch."`) {
		t.Errorf("acknowledgement missing: %s", body)
	}
	if !strings.Contains(string(body), `/repos/octocat/hello/pulls/1`) {
		t.Errorf("pull url missing: %s", body)
	}
	// The merge landed: a second update finds nothing new on the base branch.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/update-branch", fx.ownerToken, "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("re-update status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "no new commits on the base branch") {
		t.Errorf("up-to-date message missing: %s", body)
	}
}

func TestPullUpdateBranchUpToDate(t *testing.T) {
	fx := reviewServer(t)
	// The fixture's head already contains main's tip; there is nothing to merge.
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/update-branch", fx.ownerToken, "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "no new commits on the base branch") {
		t.Errorf("up-to-date message missing: %s", body)
	}
}

func TestPullUpdateBranchExpectedHeadMismatch(t *testing.T) {
	fx := reviewServer(t)
	fx.advanceMain(t, "base.txt")
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/update-branch", fx.ownerToken,
		`{"expected_head_sha":"0000000000000000000000000000000000000000"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "expected head sha") {
		t.Errorf("mismatch message missing: %s", body)
	}
}

func TestPullUpdateBranchForbidden(t *testing.T) {
	fx := reviewServer(t)
	// The reviewer can read the repository but holds no write access.
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/update-branch", fx.reviewToken, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403, body %s", resp.StatusCode, body)
	}
}
