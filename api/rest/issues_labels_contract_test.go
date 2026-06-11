package rest

import (
	"net/http"
	"strings"
	"testing"
)

// seedLabel creates a repository label as the owner so issue label operations
// have something real to attach.
func (fx issueFixture) seedLabel(t *testing.T, name, color string) {
	t.Helper()
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/labels", fx.token,
		`{"name":"`+name+`","color":"`+color+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed label %s status %d, body %s", name, resp.StatusCode, body)
	}
}

// TestIssueCreateLabelObjects covers the object form of the labels array on
// issue create: GitHub accepts strings and {"name": ...} objects mixed in the
// same array, and clients like Terraform send the object form.
func TestIssueCreateLabelObjects(t *testing.T) {
	fx := issueServer(t)
	fx.seedLabel(t, "bug", "d73a4a")
	fx.seedLabel(t, "feature", "00ff00")

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Mixed labels","labels":["bug",{"name":"feature","color":"00ff00"}]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"name":"bug"`, `"name":"feature"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("created issue missing %s: %s", want, body)
		}
	}
}

// TestIssueEditLabelObjects covers the same object form on PATCH.
func TestIssueEditLabelObjects(t *testing.T) {
	fx := issueServer(t)
	fx.seedLabel(t, "bug", "d73a4a")
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Plain"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.token,
		`{"labels":[{"name":"bug"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"name":"bug"`) {
		t.Errorf("edited issue missing bug label: %s", body)
	}
}

// TestIssueLabelsAddBareArray covers the bare-array body on POST .../labels,
// the legacy shape GitHub still accepts alongside {"labels": [...]}, with
// object members allowed in either.
func TestIssueLabelsAddBareArray(t *testing.T) {
	fx := issueServer(t)
	fx.seedLabel(t, "bug", "d73a4a")
	fx.seedLabel(t, "feature", "00ff00")
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Plain"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/labels", fx.token,
		`["bug"]`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bare array status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"name":"bug"`) {
		t.Errorf("bare array add missing bug: %s", body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/labels", fx.token,
		`{"labels":[{"name":"feature"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("object form status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"name":"feature"`) {
		t.Errorf("object form add missing feature: %s", body)
	}
}

// TestIssueSingularAssignee covers the legacy singular assignee field on
// create and edit; the plural assignees wins when both are present.
func TestIssueSingularAssignee(t *testing.T) {
	fx := issueServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Assigned","assignee":"octocat"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"assignee":{`) {
		t.Errorf("singular assignee not applied on create: %s", body)
	}

	// Clearing with assignee null removes the assignment.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.token,
		`{"assignee":null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"assignee":null`) {
		t.Errorf("singular assignee null did not clear: %s", body)
	}

	// Setting it back via PATCH with the singular string form.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.token,
		`{"assignee":"octocat"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reassign status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"assignee":{`) {
		t.Errorf("singular assignee not applied on edit: %s", body)
	}
}
