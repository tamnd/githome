package conform

import "testing"

func TestLinkRels(t *testing.T) {
	header := `<https://h/issues?page=2>; rel="next", <https://h/issues?page=5>; rel="last"`
	rels := linkRels(header)
	if !rels["next"] || !rels["last"] {
		t.Errorf("linkRels = %v, want next and last", rels)
	}
}

func TestUpstreamLeak(t *testing.T) {
	if got := upstreamLeak([]byte(`{"url":"https://git.example.com/x"}`)); got != "" {
		t.Errorf("clean body flagged: %q", got)
	}
	if got := upstreamLeak([]byte(`{"url":"https://api.github.com/x"}`)); got != "api.github.com" {
		t.Errorf("leak not caught: %q", got)
	}
}
