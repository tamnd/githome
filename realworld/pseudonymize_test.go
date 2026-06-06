package realworld

import "testing"

func TestPseudonymizeRewritesEveryLogin(t *testing.T) {
	c := sampleCorpus()
	p := NewPseudonymizer(false)
	out := p.Apply(c)

	// The owner keeps its real name (it identifies the repo, not a person), but
	// every author, assignee, and actor is rewritten to a synthetic handle.
	if out.Repo.Owner != "kubernetes" {
		t.Errorf("repo owner must not be pseudonymized: %q", out.Repo.Owner)
	}
	for _, iss := range out.Issues {
		if iss.Author == "alice" || iss.Author == "bob" {
			t.Errorf("issue author leaked a real login: %q", iss.Author)
		}
	}
	for _, ev := range out.TimelineEvents {
		if ev.Actor == "k8s-ci-bot" || ev.Actor == "carol" {
			t.Errorf("event actor leaked a real login: %q", ev.Actor)
		}
	}
	// The original corpus is left untouched.
	if c.Issues[0].Author != "alice" {
		t.Errorf("Apply mutated the input corpus: %q", c.Issues[0].Author)
	}
}

func TestPseudonymizeIsStableAndDeterministic(t *testing.T) {
	a := NewPseudonymizer(false).Apply(sampleCorpus())
	b := NewPseudonymizer(false).Apply(sampleCorpus())
	if a.Issues[0].Author != b.Issues[0].Author {
		t.Errorf("two runs disagree on the same login: %q vs %q", a.Issues[0].Author, b.Issues[0].Author)
	}

	// A login maps to one handle everywhere it appears: carol authors a comment,
	// a review, and merges the PR, so all three must carry the same pseudonym.
	p := NewPseudonymizer(false)
	out := p.Apply(sampleCorpus())
	carol := p.Mapping()["carol"]
	if carol == "" {
		t.Fatal("carol was never mapped")
	}
	if out.Comments[0].Author != carol || out.Reviews[0].Author != carol || out.PullRequests[0].MergedBy != carol {
		t.Errorf("carol mapped inconsistently: comment=%q review=%q merged_by=%q",
			out.Comments[0].Author, out.Reviews[0].Author, out.PullRequests[0].MergedBy)
	}
}

func TestPseudonymizeRedactsBodiesPreservingLength(t *testing.T) {
	c := sampleCorpus()
	want := len([]rune(c.Issues[0].Body))
	out := NewPseudonymizer(true).Apply(c)
	if out.Issues[0].Body == c.Issues[0].Body {
		t.Errorf("body not redacted: %q", out.Issues[0].Body)
	}
	if got := len([]rune(out.Issues[0].Body)); got != want {
		t.Errorf("redaction changed length: got %d want %d", got, want)
	}
}
