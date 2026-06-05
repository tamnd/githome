package rest

import (
	"net/http"
	"strings"
	"testing"
)

// The checks tests reuse the review fixture: the owner has write access and
// reports statuses and check runs against the feature branch, the reviewer has
// only read access and is refused a write.

func TestStatusCreateContract(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/statuses/feature", fx.ownerToken,
		`{"state":"success","context":"ci/build","description":"The build passed.","target_url":"https://ci.test.internal/1"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state":"success"`) {
		t.Errorf("status state missing: %s", body)
	}
	assertWriteGolden(t, "status_create.golden.json", body)
}

func TestStatusCreateForbidden(t *testing.T) {
	fx := reviewServer(t)
	// The reviewer may read the repository but has no write access.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/statuses/feature", fx.reviewToken,
		`{"state":"success","context":"ci/build"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403, body %s", resp.StatusCode, body)
	}
}

func TestStatusCreateValidation(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/statuses/feature", fx.ownerToken,
		`{"state":"bogus","context":"ci/build"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
}

func TestCombinedStatusContract(t *testing.T) {
	fx := reviewServer(t)
	fx.seedStatus(t, `{"state":"success","context":"ci/build"}`)
	fx.seedStatus(t, `{"state":"pending","context":"ci/test"}`)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/commits/feature/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	// One success and one pending fold to pending.
	if !strings.Contains(string(body), `"state":"pending"`) {
		t.Errorf("combined state not pending: %s", body)
	}
	assertWriteGolden(t, "status_combined.golden.json", body)
}

func TestStatusesListContract(t *testing.T) {
	fx := reviewServer(t)
	fx.seedStatus(t, `{"state":"success","context":"ci/build","description":"The build passed."}`)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/commits/feature/statuses")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "status_list.golden.json", body)
}

func TestCheckRunCreateContract(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"build","head_sha":"feature","status":"completed","conclusion":"success","output":{"title":"Build","summary":"It built."}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"conclusion":"success"`) {
		t.Errorf("check run conclusion missing: %s", body)
	}
	assertWriteGolden(t, "check_run_create.golden.json", body)
}

func TestCheckRunUpdateContract(t *testing.T) {
	fx := reviewServer(t)
	_, created := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"build","head_sha":"feature","status":"in_progress"}`)
	id := jsonInt(t, created, "id")
	resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/check-runs/"+itoa(id), fx.ownerToken,
		`{"status":"completed","conclusion":"success"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"status":"completed"`) {
		t.Errorf("check run not completed: %s", body)
	}
}

func TestCheckRunsListContract(t *testing.T) {
	fx := reviewServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"build","head_sha":"feature","status":"completed","conclusion":"success"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed check run status %d, body %s", resp.StatusCode, body)
	}
	resp, body := get(t, fx.srv, "/repos/octocat/hello/commits/feature/check-runs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"total_count":1`) {
		t.Errorf("check run count wrong: %s", body)
	}
	assertWriteGolden(t, "check_runs_list.golden.json", body)
}

// seedStatus posts a commit status against the feature branch as the owner.
func (fx reviewFixture) seedStatus(t *testing.T, body string) {
	t.Helper()
	resp, out := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/statuses/feature", fx.ownerToken, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed status %d, body %s", resp.StatusCode, out)
	}
}
