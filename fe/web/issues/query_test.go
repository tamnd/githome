package issues

import (
	"net/url"
	"testing"

	"github.com/tamnd/githome/fe/view"
)

func TestParseQueryDefault(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		q := ParseQuery(raw)
		if got := q.state(); got != "open" {
			t.Fatalf("ParseQuery(%q).state() = %q, want open", raw, got)
		}
		if q.Type != "issue" {
			t.Fatalf("ParseQuery(%q).Type = %q, want issue", raw, q.Type)
		}
	}
	if DefaultQuery().state() != "open" {
		t.Fatal("DefaultQuery() is not open")
	}
}

func TestParseQueryQualifiers(t *testing.T) {
	q := ParseQuery(`is:closed author:alice label:bug label:"good first issue" sort:updated-asc free text`)
	if q.state() != "closed" {
		t.Fatalf("state = %q, want closed", q.state())
	}
	if v, _ := q.firstValue("author"); v != "alice" {
		t.Fatalf("author = %q, want alice", v)
	}
	labels := q.labels()
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "good first issue" {
		t.Fatalf("labels = %#v, want [bug, good first issue]", labels)
	}
	if q.sortKey() != "updated-asc" {
		t.Fatalf("sortKey = %q, want updated-asc", q.sortKey())
	}
	if len(q.Terms) != 2 || q.Terms[0] != "free" || q.Terms[1] != "text" {
		t.Fatalf("terms = %#v, want [free, text]", q.Terms)
	}
}

func TestParseQueryNegationAndNo(t *testing.T) {
	q := ParseQuery(`-label:wontfix no:assignee`)
	if !q.hasNo("assignee") {
		t.Fatal("no:assignee not parsed")
	}
	// A negated label is not part of the active AND set.
	if len(q.labels()) != 0 {
		t.Fatalf("labels = %#v, want empty (negated)", q.labels())
	}
}

func TestParseQueryIsPR(t *testing.T) {
	q := ParseQuery(`is:pr is:open`)
	if q.Type != "pr" {
		t.Fatalf("Type = %q, want pr", q.Type)
	}
}

func TestFilterProjection(t *testing.T) {
	q := ParseQuery(`is:closed author:bob assignee:@me label:bug milestone:3 sort:comments-asc`)
	viewer := &view.Viewer{Login: "me-login"}
	f := q.Filter(viewer)
	if f.State != "closed" {
		t.Fatalf("State = %q, want closed", f.State)
	}
	if f.CreatorLogin != "bob" {
		t.Fatalf("CreatorLogin = %q, want bob", f.CreatorLogin)
	}
	if f.AssigneeLogin != "me-login" {
		t.Fatalf("AssigneeLogin = %q, want me-login (@me rewrite)", f.AssigneeLogin)
	}
	if len(f.Labels) != 1 || f.Labels[0] != "bug" {
		t.Fatalf("Labels = %#v, want [bug]", f.Labels)
	}
	if f.MilestoneNumber == nil || *f.MilestoneNumber != 3 {
		t.Fatalf("MilestoneNumber = %v, want 3", f.MilestoneNumber)
	}
	if f.Sort != "comments" || f.Direction != "asc" {
		t.Fatalf("Sort/Direction = %q/%q, want comments/asc", f.Sort, f.Direction)
	}
}

func TestFilterMeWithoutViewer(t *testing.T) {
	q := ParseQuery(`assignee:@me`)
	f := q.Filter(nil)
	if f.AssigneeLogin != "@me" {
		t.Fatalf("AssigneeLogin = %q, want literal @me for anon", f.AssigneeLogin)
	}
}

func TestFilterMilestoneNonNumeric(t *testing.T) {
	q := ParseQuery(`milestone:v1.0`)
	f := q.Filter(nil)
	if f.MilestoneNumber != nil {
		t.Fatalf("MilestoneNumber = %v, want nil for non-numeric title", f.MilestoneNumber)
	}
}

func TestComposerRoundTrip(t *testing.T) {
	q := ParseQuery(`is:open label:bug`)
	href := q.WithState("closed")
	got := decodeQ(t, href)
	want := `is:issue is:closed label:bug`
	if got != want {
		t.Fatalf("WithState href q = %q, want %q", got, want)
	}
	// The original query is untouched by composition.
	if q.state() != "open" {
		t.Fatalf("compose mutated source state to %q", q.state())
	}
}

func TestComposerAddRemoveLabel(t *testing.T) {
	q := ParseQuery(`is:open`)
	add := decodeQ(t, q.AddLabel("bug"))
	if add != `is:issue is:open label:bug` {
		t.Fatalf("AddLabel q = %q", add)
	}
	// Adding a present label is idempotent.
	q2 := ParseQuery(`is:open label:bug`)
	if decodeQ(t, q2.AddLabel("bug")) != `is:issue is:open label:bug` {
		t.Fatal("AddLabel not idempotent")
	}
	rm := decodeQ(t, q2.RemoveLabel("bug"))
	if rm != `is:issue is:open` {
		t.Fatalf("RemoveLabel q = %q", rm)
	}
}

func TestComposerQuotesMultiWord(t *testing.T) {
	q := ParseQuery(`is:open`)
	href := q.SetMilestone("v1 final")
	got := decodeQ(t, href)
	if got != `is:issue is:open milestone:"v1 final"` {
		t.Fatalf("SetMilestone q = %q", got)
	}
	// And it parses back to the same single value.
	if v, _ := ParseQuery(got).firstValue("milestone"); v != "v1 final" {
		t.Fatalf("round-trip milestone = %q, want v1 final", v)
	}
}

func TestComposerSetSortOmitsDefault(t *testing.T) {
	q := ParseQuery(`is:open sort:updated-desc`)
	got := decodeQ(t, q.SetSort(defaultSort))
	if got != `is:issue is:open` {
		t.Fatalf("SetSort(default) q = %q, want bare default", got)
	}
}

func TestComposerSetAssigneeClearsNo(t *testing.T) {
	q := ParseQuery(`is:open no:assignee`)
	got := decodeQ(t, q.SetAssignee("carol"))
	if got != `is:issue is:open assignee:carol` {
		t.Fatalf("SetAssignee q = %q, want no:assignee dropped", got)
	}
}

// decodeQ pulls the q value out of a composed ?q=... href and decodes it.
func decodeQ(t *testing.T, href string) string {
	t.Helper()
	u, err := url.Parse(href)
	if err != nil {
		t.Fatalf("parse href %q: %v", href, err)
	}
	return u.Query().Get("q")
}
