package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestBranchProtectionRoundTrip covers PUT then GET on branch protection: the
// required_status_checks contexts and the restrictions object must survive the
// trip instead of collapsing to empty defaults.
func TestBranchProtectionRoundTrip(t *testing.T) {
	fx := repoServer(t)

	put := `{
		"required_status_checks": {"strict": true, "contexts": ["ci/build", "ci/test"]},
		"enforce_admins": true,
		"required_pull_request_reviews": {"dismiss_stale_reviews": true, "required_approving_review_count": 2},
		"restrictions": {"users": ["octocat"], "teams": ["justice-league"]}
	}`
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/branches/master/protection", fx.token, put)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put status %d, body %s", resp.StatusCode, body)
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/branches/master/protection", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d, body %s", resp.StatusCode, body)
	}

	var doc struct {
		RequiredStatusChecks struct {
			Strict   bool     `json:"strict"`
			Contexts []string `json:"contexts"`
			Checks   []struct {
				Context string `json:"context"`
			} `json:"checks"`
		} `json:"required_status_checks"`
		EnforceAdmins struct {
			Enabled bool `json:"enabled"`
		} `json:"enforce_admins"`
		RequiredPullRequestReviews struct {
			DismissStaleReviews          bool `json:"dismiss_stale_reviews"`
			RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
		} `json:"required_pull_request_reviews"`
		Restrictions *struct {
			Users []struct {
				Login string `json:"login"`
			} `json:"users"`
			Teams []struct {
				Slug string `json:"slug"`
			} `json:"teams"`
			Apps []any `json:"apps"`
		} `json:"restrictions"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, body)
	}

	if !doc.RequiredStatusChecks.Strict {
		t.Error("strict not round-tripped")
	}
	if got := doc.RequiredStatusChecks.Contexts; len(got) != 2 || got[0] != "ci/build" || got[1] != "ci/test" {
		t.Errorf("contexts = %v, want [ci/build ci/test]", got)
	}
	if len(doc.RequiredStatusChecks.Checks) != 2 || doc.RequiredStatusChecks.Checks[0].Context != "ci/build" {
		t.Errorf("checks = %+v", doc.RequiredStatusChecks.Checks)
	}
	if !doc.EnforceAdmins.Enabled {
		t.Error("enforce_admins not round-tripped")
	}
	if !doc.RequiredPullRequestReviews.DismissStaleReviews || doc.RequiredPullRequestReviews.RequiredApprovingReviewCount != 2 {
		t.Errorf("required_pull_request_reviews = %+v", doc.RequiredPullRequestReviews)
	}
	if doc.Restrictions == nil {
		t.Fatal("restrictions came back null")
	}
	if len(doc.Restrictions.Users) != 1 || doc.Restrictions.Users[0].Login != "octocat" {
		t.Errorf("restriction users = %+v", doc.Restrictions.Users)
	}
	if len(doc.Restrictions.Teams) != 1 || doc.Restrictions.Teams[0].Slug != "justice-league" {
		t.Errorf("restriction teams = %+v", doc.Restrictions.Teams)
	}
	if doc.Restrictions.Apps == nil {
		t.Error("restriction apps missing, want empty array")
	}

	// Without a restrictions object, the GET reports null, not an empty set.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/branches/master/protection", fx.token,
		`{"required_status_checks": {"strict": false, "contexts": []}, "enforce_admins": false,
		  "required_pull_request_reviews": null, "restrictions": null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second put status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/branches/master/protection", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second get status %d, body %s", resp.StatusCode, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["restrictions"]) != "null" {
		t.Errorf("restrictions = %s, want null", raw["restrictions"])
	}
	var rsc struct {
		Contexts []string `json:"contexts"`
	}
	if err := json.Unmarshal(raw["required_status_checks"], &rsc); err != nil {
		t.Fatalf("unmarshal required_status_checks: %v", err)
	}
	if rsc.Contexts == nil || len(rsc.Contexts) != 0 {
		t.Errorf("contexts = %v, want empty array", rsc.Contexts)
	}
}

// protectionPut seeds a full protection rule on master and fails the test on
// any non-200.
func protectionPut(t *testing.T, fx repoFixture) {
	t.Helper()
	put := `{
		"required_status_checks": {"strict": true, "contexts": ["ci/build"]},
		"enforce_admins": true,
		"required_pull_request_reviews": {"dismiss_stale_reviews": true, "required_approving_review_count": 2},
		"restrictions": {"users": ["octocat"], "teams": ["justice-league"]}
	}`
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/branches/master/protection", fx.token, put)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed put status %d, body %s", resp.StatusCode, body)
	}
}

// TestBranchProtectionGetShape pins the GET response to GitHub's full shape:
// real url fields on the object and its sub-objects plus the enabled-wrapper
// fields the original implementation omitted.
func TestBranchProtectionGetShape(t *testing.T) {
	fx := repoServer(t)
	protectionPut(t, fx)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/branches/master/protection", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d, body %s", resp.StatusCode, body)
	}
	base := `/repos/octocat/hello/branches/master/protection`
	for _, want := range []string{
		`"url":"`, base + `"`,
		base + `/required_status_checks"`,
		`"contexts_url":"`, base + `/required_status_checks/contexts"`,
		base + `/enforce_admins"`,
		base + `/required_pull_request_reviews"`,
		base + `/restrictions"`,
		`"users_url"`, `"teams_url"`, `"apps_url"`,
		`"required_signatures"`, `"required_linear_history"`,
		`"allow_force_pushes"`, `"allow_deletions"`, `"block_creations"`,
		`"required_conversation_resolution"`, `"lock_branch"`, `"allow_fork_syncing"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("protection GET missing %s: %s", want, body)
		}
	}

	// The PUT contract: every one of the four top-level keys must be present.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/branches/master/protection", fx.token,
		`{"required_status_checks": null, "enforce_admins": null, "required_pull_request_reviews": null}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("partial put status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `\"restrictions\" wasn't supplied`) {
		t.Errorf("partial put message: %s", body)
	}

	// The optional toggles round-trip.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/branches/master/protection", fx.token,
		`{"required_status_checks": null, "enforce_admins": null, "required_pull_request_reviews": null,
		  "restrictions": null, "required_linear_history": true, "lock_branch": true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle put status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"required_linear_history":{"enabled":true}`,
		`"lock_branch":{"enabled":true}`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("toggle put missing %s: %s", want, body)
		}
	}
}

// TestProtectionToggleSubEndpoints covers enforce_admins and
// required_signatures: GET reads the flag, POST enables, DELETE disables.
func TestProtectionToggleSubEndpoints(t *testing.T) {
	fx := repoServer(t)
	protectionPut(t, fx)
	base := "/repos/octocat/hello/branches/master/protection"

	for _, slug := range []string{"enforce_admins", "required_signatures"} {
		resp, body := authedSend(t, fx.srv, http.MethodPost, base+"/"+slug, fx.token, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s post status %d, body %s", slug, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), `"enabled":true`) {
			t.Errorf("%s post body: %s", slug, body)
		}
		resp, body = authedSend(t, fx.srv, http.MethodDelete, base+"/"+slug, fx.token, "")
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s delete status %d, body %s", slug, resp.StatusCode, body)
		}
		resp, body = authedGet(t, fx.srv, base+"/"+slug, "token "+fx.token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s get status %d", slug, resp.StatusCode)
		}
		if !strings.Contains(string(body), `"enabled":false`) {
			t.Errorf("%s get after delete: %s", slug, body)
		}
	}
}

// TestProtectionStatusChecksSubEndpoints covers required_status_checks GET,
// PATCH, DELETE and the contexts list CRUD.
func TestProtectionStatusChecksSubEndpoints(t *testing.T) {
	fx := repoServer(t)
	protectionPut(t, fx)
	base := "/repos/octocat/hello/branches/master/protection/required_status_checks"

	resp, body := authedGet(t, fx.srv, base, "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"strict":true`, `"contexts":["ci/build"]`, `"contexts_url"`, `"checks":[{`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("status checks GET missing %s: %s", want, body)
		}
	}

	// PATCH strict only; contexts stay.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, base, fx.token, `{"strict": false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"strict":false`) || !strings.Contains(string(body), `"ci/build"`) {
		t.Errorf("patch result: %s", body)
	}

	// Contexts CRUD: POST appends, PUT replaces, DELETE removes.
	resp, body = authedSend(t, fx.srv, http.MethodPost, base+"/contexts", fx.token, `["ci/lint"]`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("contexts post status %d, body %s", resp.StatusCode, body)
	}
	if string(body) != `["ci/build","ci/lint"]` {
		t.Errorf("contexts after post: %s", body)
	}
	resp, body = authedSend(t, fx.srv, http.MethodPut, base+"/contexts", fx.token, `{"contexts":["ci/test"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("contexts put status %d, body %s", resp.StatusCode, body)
	}
	if string(body) != `["ci/test"]` {
		t.Errorf("contexts after put: %s", body)
	}
	resp, body = authedSend(t, fx.srv, http.MethodDelete, base+"/contexts", fx.token, `["ci/test"]`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("contexts delete status %d, body %s", resp.StatusCode, body)
	}
	if string(body) != "[]" {
		t.Errorf("contexts after delete: %s", body)
	}

	// DELETE the requirement; GET then 404s.
	resp, _ = authedSend(t, fx.srv, http.MethodDelete, base, fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d, want 204", resp.StatusCode)
	}
	resp, body = authedGet(t, fx.srv, base, "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Required status checks not enabled") {
		t.Errorf("404 message: %s", body)
	}
}

// TestProtectionReviewsAndRestrictionsSubEndpoints covers
// required_pull_request_reviews PATCH/DELETE and the restrictions object with
// its users/teams/apps lists.
func TestProtectionReviewsAndRestrictionsSubEndpoints(t *testing.T) {
	fx := repoServer(t)
	protectionPut(t, fx)
	base := "/repos/octocat/hello/branches/master/protection"

	resp, body := authedSend(t, fx.srv, http.MethodPatch, base+"/required_pull_request_reviews", fx.token,
		`{"required_approving_review_count": 1}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reviews patch status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"required_approving_review_count":1`) ||
		!strings.Contains(string(body), `"dismiss_stale_reviews":true`) {
		t.Errorf("reviews patch result: %s", body)
	}

	resp, body = authedGet(t, fx.srv, base+"/restrictions", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restrictions get status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"octocat"`, `"justice-league"`, `"apps":[]`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("restrictions GET missing %s: %s", want, body)
		}
	}

	// Users list CRUD.
	resp, body = authedSend(t, fx.srv, http.MethodPut, base+"/restrictions/users", fx.token, `{"users":["octocat"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("users put status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedSend(t, fx.srv, http.MethodDelete, base+"/restrictions/users", fx.token, `["octocat"]`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("users delete status %d, body %s", resp.StatusCode, body)
	}
	if string(body) != "[]" {
		t.Errorf("users after delete: %s", body)
	}

	// Teams list keeps slugs.
	resp, body = authedSend(t, fx.srv, http.MethodPost, base+"/restrictions/teams", fx.token, `["avengers"]`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("teams post status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"avengers"`) || !strings.Contains(string(body), `"justice-league"`) {
		t.Errorf("teams after post: %s", body)
	}

	// Apps are accepted but always empty.
	resp, body = authedGet(t, fx.srv, base+"/restrictions/apps", "token "+fx.token)
	if resp.StatusCode != http.StatusOK || string(body) != "[]" {
		t.Fatalf("apps get status %d, body %s", resp.StatusCode, body)
	}

	// Reviews DELETE drops the requirement; GET then 404s.
	resp, _ = authedSend(t, fx.srv, http.MethodDelete, base+"/required_pull_request_reviews", fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reviews delete status %d, want 204", resp.StatusCode)
	}
	resp, _ = authedGet(t, fx.srv, base+"/required_pull_request_reviews", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reviews get after delete status %d", resp.StatusCode)
	}

	// Restrictions DELETE removes the object; GET then 404s and the parent
	// object reports null.
	resp, _ = authedSend(t, fx.srv, http.MethodDelete, base+"/restrictions", fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("restrictions delete status %d, want 204", resp.StatusCode)
	}
	resp, _ = authedGet(t, fx.srv, base+"/restrictions", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("restrictions get after delete status %d", resp.StatusCode)
	}
	resp, body = authedGet(t, fx.srv, base, "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("protection get status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"restrictions":null`) {
		t.Errorf("parent restrictions not null after delete: %s", body)
	}
}
