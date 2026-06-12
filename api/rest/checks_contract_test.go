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

func TestCheckRunsListFilters(t *testing.T) {
	fx := reviewServer(t)
	for _, in := range []string{
		`{"name":"build","head_sha":"feature","status":"completed","conclusion":"failure"}`,
		`{"name":"build","head_sha":"feature","status":"completed","conclusion":"success"}`,
		`{"name":"test","head_sha":"feature","status":"queued"}`,
	} {
		if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken, in); resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed check run status %d, body %s", resp.StatusCode, body)
		}
	}
	// The default filter keeps only the latest attempt per name.
	_, body := get(t, fx.srv, "/repos/octocat/hello/commits/feature/check-runs")
	if !strings.Contains(string(body), `"total_count":2`) {
		t.Errorf("latest filter count wrong: %s", body)
	}
	if strings.Contains(string(body), `"conclusion":"failure"`) {
		t.Errorf("latest filter kept the superseded attempt: %s", body)
	}
	_, body = get(t, fx.srv, "/repos/octocat/hello/commits/feature/check-runs?filter=all")
	if !strings.Contains(string(body), `"total_count":3`) {
		t.Errorf("filter=all count wrong: %s", body)
	}
	_, body = get(t, fx.srv, "/repos/octocat/hello/commits/feature/check-runs?check_name=test")
	if !strings.Contains(string(body), `"total_count":1`) || strings.Contains(string(body), `"name":"build"`) {
		t.Errorf("check_name filter wrong: %s", body)
	}
	_, body = get(t, fx.srv, "/repos/octocat/hello/commits/feature/check-runs?status=queued")
	if !strings.Contains(string(body), `"total_count":1`) || !strings.Contains(string(body), `"name":"test"`) {
		t.Errorf("status filter wrong: %s", body)
	}
	_, body = get(t, fx.srv, "/repos/octocat/hello/commits/feature/check-runs?filter=all&per_page=1&page=2")
	if !strings.Contains(string(body), `"total_count":3`) || strings.Count(string(body), `"head_sha"`) != 1 {
		t.Errorf("check runs page window wrong: %s", body)
	}
}

func TestCheckSuiteCreateAndGetContract(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-suites", fx.ownerToken,
		`{"head_sha":"feature"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	id := jsonInt(t, body, "id")
	if id == 0 {
		t.Fatalf("suite id is zero: %s", body)
	}
	// Re-creating for the same head returns the same persisted suite.
	if _, again := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-suites", fx.ownerToken,
		`{"head_sha":"feature"}`); jsonInt(t, again, "id") != id {
		t.Fatalf("re-create changed the suite id: %s then %s", body, again)
	}
	resp, body = get(t, fx.srv, "/repos/octocat/hello/check-suites/"+itoa(id))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("suite get status %d, body %s", resp.StatusCode, body)
	}
	if jsonInt(t, body, "id") != id {
		t.Errorf("suite get id mismatch: %s", body)
	}
	// A run reported against the same head lands in this suite and references
	// it by the same public id.
	_, run := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"build","head_sha":"feature"}`)
	if !strings.Contains(string(run), `"check_suite":{"id":`+itoa(id)+`}`) {
		t.Errorf("run check_suite ref does not round-trip: %s", run)
	}
	resp, body = get(t, fx.srv, "/repos/octocat/hello/check-suites/"+itoa(id)+"/check-runs")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"total_count":1`) {
		t.Errorf("suite check-runs status %d body %s", resp.StatusCode, body)
	}
}

func TestCheckSuiteCreateValidation(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-suites", fx.ownerToken, `{}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
}

func TestCheckRunRerequestContract(t *testing.T) {
	fx := reviewServer(t)
	_, created := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"build","head_sha":"feature","status":"completed","conclusion":"failure"}`)
	id := jsonInt(t, created, "id")
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs/"+itoa(id)+"/rerequest", fx.ownerToken, ``)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	_, run := get(t, fx.srv, "/repos/octocat/hello/check-runs/"+itoa(id))
	if !strings.Contains(string(run), `"status":"queued"`) || !strings.Contains(string(run), `"conclusion":null`) {
		t.Errorf("rerequested run not reset: %s", run)
	}
}

func TestCheckSuiteRerequestContract(t *testing.T) {
	fx := reviewServer(t)
	_, suite := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-suites", fx.ownerToken,
		`{"head_sha":"feature"}`)
	sid := jsonInt(t, suite, "id")
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"build","head_sha":"feature","status":"completed","conclusion":"success"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed check run status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-suites/"+itoa(sid)+"/rerequest", fx.ownerToken, ``)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	_, after := get(t, fx.srv, "/repos/octocat/hello/check-suites/"+itoa(sid))
	if !strings.Contains(string(after), `"status":"queued"`) {
		t.Errorf("rerequested suite not queued: %s", after)
	}
}

func TestCheckRunRerequestForbidden(t *testing.T) {
	fx := reviewServer(t)
	_, created := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"build","head_sha":"feature"}`)
	id := jsonInt(t, created, "id")
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs/"+itoa(id)+"/rerequest", fx.reviewToken, ``)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403, body %s", resp.StatusCode, body)
	}
}

func TestCheckSuitePreferencesContract(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/check-suites/preferences", fx.ownerToken,
		`{"auto_trigger_checks":[{"app_id":1,"setting":false}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"auto_trigger_checks"`) || !strings.Contains(string(body), `"repository"`) {
		t.Errorf("preferences shape wrong: %s", body)
	}
}

// seedStatus posts a commit status against the feature branch as the owner.
func (fx reviewFixture) seedStatus(t *testing.T, body string) {
	t.Helper()
	resp, out := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/statuses/feature", fx.ownerToken, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed status %d, body %s", resp.StatusCode, out)
	}
}
