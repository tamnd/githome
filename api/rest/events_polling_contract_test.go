package rest

import (
	"net/http"
	"testing"
)

// TestEventsPollingContract checks the documented polling contract the review
// asked for: every feed advertises an X-Poll-Interval pacing hint and a body
// ETag, and a conditional re-poll carrying that ETag short-circuits to 304 Not
// Modified so a poller stops hot-looping.
func TestEventsPollingContract(t *testing.T) {
	fx := eventServer(t)
	fx.seedIssueEvent(t)

	resp, body := get(t, fx.srv, "/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Poll-Interval"); got != "60" {
		t.Errorf("X-Poll-Interval = %q, want 60", got)
	}
	tag := resp.Header.Get("ETag")
	if tag == "" {
		t.Fatalf("feed missing ETag header")
	}

	// A re-poll carrying the feed's validator is answered 304 with no body.
	req, err := http.NewRequest(http.MethodGet, fx.srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("If-None-Match", tag)
	cond, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional poll: %v", err)
	}
	defer func() { _ = cond.Body.Close() }()
	if cond.StatusCode != http.StatusNotModified {
		t.Errorf("conditional poll status %d, want 304", cond.StatusCode)
	}
	if cond.Header.Get("X-Poll-Interval") != "60" {
		t.Errorf("304 dropped X-Poll-Interval header")
	}
}

// TestEventFeedEndpoints checks the feed routes the review found missing all
// exist and serve an event array: a user's public feed, an org timeline, the
// repository network timeline, and the received-events feed (and its public
// twin). With one recorded issue event each feed that includes it is non-empty.
func TestEventFeedEndpoints(t *testing.T) {
	fx := eventServer(t)
	fx.seedIssueEvent(t)

	for _, path := range []string{
		"/users/octocat/events/public",
		"/orgs/octocat/events",
		"/networks/octocat/hello/events",
		"/users/octocat/received_events",
		"/users/octocat/received_events/public",
	} {
		resp, body := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200, body %s", path, resp.StatusCode, body)
			continue
		}
		if resp.Header.Get("X-Poll-Interval") != "60" {
			t.Errorf("%s: missing X-Poll-Interval", path)
		}
		if string(body) == "[]" {
			t.Errorf("%s: feed empty, want the recorded event", path)
		}
	}
}

// TestEventFeedEndpointErrors checks the new feeds reject a missing subject the
// same way the existing feeds do: a 404 for an account that does not exist.
func TestEventFeedEndpointErrors(t *testing.T) {
	fx := eventServer(t)

	for _, path := range []string{
		"/users/ghost/events/public",
		"/orgs/ghost/events",
		"/users/ghost/received_events",
		"/networks/octocat/nope/events",
	} {
		if resp, _ := get(t, fx.srv, path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
}
