package rest

import (
	"net/http"
	"testing"
)

// TestSearchTextMatch covers the text-match media support the compat review
// flagged as missing (R01-53): when the Accept header asks for the text-match
// type, each hit carries text_matches with the matched property, fragment, and
// rune offsets; without it, no text_matches appear.
func TestSearchTextMatch(t *testing.T) {
	fx := searchServer(t)
	fx.seedIssue(t, "hello", `{"title":"Login crashes on start","body":"It crashes when I open the app."}`)

	const tm = "application/vnd.github.text-match+json"

	// Without the media type the field is absent.
	resp, body := get(t, fx.srv, "/search/issues?q=crashes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plain search: status %d, body %s", resp.StatusCode, body)
	}
	if contains(body, `"text_matches"`) {
		t.Errorf("plain search should omit text_matches: %s", body)
	}

	// With the media type each hit carries text_matches over title and body.
	resp, body = getWith(t, fx.srv, "/search/issues?q=crashes", map[string]string{"Accept": tm})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("text-match search: status %d, body %s", resp.StatusCode, body)
	}
	var issues struct {
		Items []struct {
			Title       string `json:"title"`
			TextMatches []struct {
				ObjectType *string `json:"object_type"`
				Property   string  `json:"property"`
				Fragment   string  `json:"fragment"`
				Matches    []struct {
					Text    string `json:"text"`
					Indices []int  `json:"indices"`
				} `json:"matches"`
			} `json:"text_matches"`
		} `json:"items"`
	}
	decodeBody(t, body, &issues)
	if len(issues.Items) != 1 {
		t.Fatalf("want 1 hit, got %d: %s", len(issues.Items), body)
	}
	tms := issues.Items[0].TextMatches
	if len(tms) == 0 {
		t.Fatalf("text_matches empty: %s", body)
	}
	// The body property fragment is the issue body, and the matched substring
	// is the queried term with sane rune offsets into that fragment.
	var bodyMatch bool
	for _, m := range tms {
		if m.ObjectType == nil || *m.ObjectType != "Issue" {
			t.Errorf("object_type = %v, want Issue", m.ObjectType)
		}
		if m.Property == "body" {
			bodyMatch = true
			if m.Fragment != "It crashes when I open the app." {
				t.Errorf("body fragment = %q", m.Fragment)
			}
			if len(m.Matches) == 0 {
				t.Fatalf("body has no matches: %s", body)
			}
			one := m.Matches[0]
			if one.Text != "crashes" {
				t.Errorf("matched text = %q, want crashes", one.Text)
			}
			if len(one.Indices) != 2 || m.Fragment[one.Indices[0]:one.Indices[1]] != "crashes" {
				t.Errorf("indices %v do not bound %q in fragment", one.Indices, one.Text)
			}
		}
	}
	if !bodyMatch {
		t.Errorf("expected a body text match: %s", body)
	}
}

// TestSearchCodeScopeError checks the unscoped code-search 422 carries GitHub's
// dotcom wording so clients that match on the message do not diverge.
func TestSearchCodeScopeError(t *testing.T) {
	fx := searchServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodGet, "/search/code?q=needle", fx.token, "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unscoped code search: status %d, body %s", resp.StatusCode, body)
	}
	if !contains(body, "Must include at least one user, organization, or repository") {
		t.Errorf("422 message diverges from dotcom: %s", body)
	}
}
