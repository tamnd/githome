package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The check run details a reporter writes must read back: explicit started_at
// and completed_at, the requested actions, and the output annotations with
// their count. The Jenkins checks plugin writes these and re-reads them.

func TestCheckRunDetailsRoundTrip(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"lint","head_sha":"feature","status":"completed","conclusion":"neutral",
		  "started_at":"2026-06-12T08:00:00Z","completed_at":"2026-06-12T08:05:00Z",
		  "actions":[{"label":"Fix","description":"Apply the fix.","identifier":"fix"}],
		  "output":{"title":"Lint","summary":"2 problems.","annotations":[
		    {"path":"feature.txt","start_line":1,"end_line":1,"annotation_level":"warning","message":"First problem.","title":"W1"},
		    {"path":"feature.txt","start_line":1,"end_line":1,"annotation_level":"failure","message":"Second problem."}
		  ]}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"started_at":"2026-06-12T08:00:00Z"`,
		`"completed_at":"2026-06-12T08:05:00Z"`,
		`"annotations_count":2`,
		`"identifier":"fix"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("create response missing %s: %s", want, body)
		}
	}
	id := jsonInt(t, body, "id")

	// The run reads back with the same details.
	resp, body = get(t, fx.srv, "/repos/octocat/hello/check-runs/"+itoa(id))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-read status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"started_at":"2026-06-12T08:00:00Z"`) {
		t.Errorf("started_at lost on re-read: %s", body)
	}
	if !strings.Contains(string(body), `"identifier":"fix"`) {
		t.Errorf("actions lost on re-read: %s", body)
	}

	// The annotations endpoint returns the batch in write order.
	resp, body = get(t, fx.srv, "/repos/octocat/hello/check-runs/"+itoa(id)+"/annotations")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("annotations status %d, body %s", resp.StatusCode, body)
	}
	var anns []map[string]any
	if err := json.Unmarshal(body, &anns); err != nil {
		t.Fatalf("decode annotations: %v\n%s", err, body)
	}
	if len(anns) != 2 {
		t.Fatalf("annotations = %d, want 2: %s", len(anns), body)
	}
	if anns[0]["message"] != "First problem." || anns[1]["message"] != "Second problem." {
		t.Errorf("annotation order wrong: %s", body)
	}
	if !strings.Contains(string(body), `"blob_href"`) {
		t.Errorf("blob_href missing: %s", body)
	}

	// A later update appends annotations rather than replacing them.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/check-runs/"+itoa(id), fx.ownerToken,
		`{"output":{"title":"Lint","summary":"3 problems.","annotations":[
		   {"path":"feature.txt","start_line":1,"end_line":1,"annotation_level":"notice","message":"Third problem."}]}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"annotations_count":3`) {
		t.Errorf("annotations did not accumulate: %s", body)
	}
}

func TestCheckRunAnnotationValidation(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/check-runs", fx.ownerToken,
		`{"name":"lint","head_sha":"feature","output":{"title":"Lint","summary":"s","annotations":[
		   {"path":"feature.txt","start_line":1,"end_line":1,"annotation_level":"fatal","message":"Bad level."}]}}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
}

func TestCheckRunAnnotationsUnknownRun(t *testing.T) {
	fx := reviewServer(t)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/check-runs/999999/annotations")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404, body %s", resp.StatusCode, body)
	}
}
