package realworld

import (
	"fmt"
	"maps"
	"strings"
)

// Pseudonymizer rewrites a corpus so it carries no real identities: every person
// login becomes a stable synthetic handle, and, when RedactBodies is set, every
// free-text body becomes a length-preserving placeholder. It is a pure
// transform — same input, same output — so a pseudonymized corpus is as
// reproducible as the original, and the mapping is recorded so a captured
// response can be compared field for field.
//
// The repository's own owner and name are not pseudonymized: they identify the
// repository, not a person, and the manifest already records them. A login that
// also happens to be the owner is still rewritten where it appears as an author,
// actor, or assignee, so no real handle survives in the issue and event bodies.
type Pseudonymizer struct {
	// RedactBodies replaces issue, comment, and review bodies with a
	// length-preserving placeholder so the corpus carries no real prose while
	// the marshaled-payload size stays realistic.
	RedactBodies bool

	mapping map[string]string
	order   []string
}

// NewPseudonymizer returns a pseudonymizer with an empty mapping.
func NewPseudonymizer(redactBodies bool) *Pseudonymizer {
	return &Pseudonymizer{RedactBodies: redactBodies, mapping: map[string]string{}}
}

// Mapping returns the login-to-pseudonym map built so far, copied so the caller
// cannot mutate the pseudonymizer's state.
func (p *Pseudonymizer) Mapping() map[string]string {
	out := make(map[string]string, len(p.mapping))
	maps.Copy(out, p.mapping)
	return out
}

// pseudo returns the stable pseudonym for a login, assigning the next one in
// first-seen order on first sight. First-seen order over the corpus's
// deterministic Logins() walk makes the assignment reproducible.
func (p *Pseudonymizer) pseudo(login string) string {
	if login == "" {
		return ""
	}
	if v, ok := p.mapping[login]; ok {
		return v
	}
	v := fmt.Sprintf("user-%d", len(p.order)+1)
	p.mapping[login] = v
	p.order = append(p.order, login)
	return v
}

// Apply returns a pseudonymized copy of the corpus. The original is left
// unchanged. Logins are assigned in the corpus's first-seen order so the mapping
// is deterministic.
func (p *Pseudonymizer) Apply(c *Corpus) *Corpus {
	// Prime the mapping in first-seen order before rewriting any field, so the
	// assignment does not depend on which field a login is rewritten in first.
	for _, login := range c.Logins() {
		p.pseudo(login)
	}

	out := *c // shallow copy; every slice is rebuilt below so the original is untouched
	out.Issues = make([]Issue, len(c.Issues))
	for i, iss := range c.Issues {
		iss.Author = p.pseudo(iss.Author)
		if len(iss.Assignees) > 0 {
			as := make([]string, len(iss.Assignees))
			for j, a := range iss.Assignees {
				as[j] = p.pseudo(a)
			}
			iss.Assignees = as
		}
		iss.Body = p.body(iss.Body)
		out.Issues[i] = iss
	}
	out.PullRequests = make([]PullRequest, len(c.PullRequests))
	for i, pr := range c.PullRequests {
		pr.MergedBy = p.pseudo(pr.MergedBy)
		out.PullRequests[i] = pr
	}
	out.Comments = make([]Comment, len(c.Comments))
	for i, cm := range c.Comments {
		cm.Author = p.pseudo(cm.Author)
		cm.Body = p.body(cm.Body)
		out.Comments[i] = cm
	}
	out.Reviews = make([]Review, len(c.Reviews))
	for i, r := range c.Reviews {
		r.Author = p.pseudo(r.Author)
		r.Body = p.body(r.Body)
		out.Reviews[i] = r
	}
	out.ReviewComments = make([]ReviewComment, len(c.ReviewComments))
	for i, rc := range c.ReviewComments {
		rc.Author = p.pseudo(rc.Author)
		rc.Body = p.body(rc.Body)
		out.ReviewComments[i] = rc
	}
	out.TimelineEvents = make([]TimelineEvent, len(c.TimelineEvents))
	for i, ev := range c.TimelineEvents {
		ev.Actor = p.pseudo(ev.Actor)
		ev.Assignee = p.pseudo(ev.Assignee)
		out.TimelineEvents[i] = ev
	}
	// PR files and commit statuses name no people, so they pass through.
	return &out
}

// body returns a length-preserving redaction of a body when RedactBodies is set,
// or the body unchanged otherwise. Preserving the rune count keeps the marshaled
// payload size, and therefore the marshal and ETag cost, close to the original
// while carrying none of the real prose.
func (p *Pseudonymizer) body(s string) string {
	if !p.RedactBodies || s == "" {
		return s
	}
	n := len([]rune(s))
	return strings.Repeat("x", n)
}
