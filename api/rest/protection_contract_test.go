package rest

import (
	"encoding/json"
	"net/http"
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
		`{"required_status_checks": {"strict": false, "contexts": []}, "enforce_admins": false}`)
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
