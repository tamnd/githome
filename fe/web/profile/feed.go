package profile

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// feed.go is the activity catalog: it maps one stored event into the one line the
// timeline shows. The domain event carries the Events-API type and the rendered
// payload the fan-out worker stored, so this file reads the type for the glyph and
// the verb and parses the payload for the action and the target (an issue or pull
// number with its title). A payload that has not been rendered yet, or an event
// type the catalog does not know, degrades to a plain "had activity in" line
// rather than dropping the event, so the timeline never silently loses an entry.
// See implementation/12 section 5.

// feedPayload is the slice of the Events-API payload the timeline reads: the
// action that refines the verb and the icon, the push ref the branch chip comes
// from, and the issue or pull subject the target points at. Every field is
// optional; an absent field leaves the line at its type default.
type feedPayload struct {
	Action      string       `json:"action"`
	Ref         string       `json:"ref"`
	Issue       *feedSubject `json:"issue"`
	PullRequest *feedSubject `json:"pull_request"`
}

// feedSubject is the issue or pull a payload points at: the number that builds the
// link and the title that reads as the subject.
type feedSubject struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// FeedItems maps a page of events into timeline lines, skipping any event the
// catalog cannot place (one missing its actor or repository, which the feed never
// produces but the mapper guards against rather than panicking). It is exported
// because the dashboard's recent-activity feed reads the same stored events, and
// one catalog keeps the two timelines telling the same story for the same event.
func FeedItems(events []domain.Event) []view.FeedItemVM {
	out := make([]view.FeedItemVM, 0, len(events))
	for i := range events {
		if item, ok := feedItem(&events[i]); ok {
			out = append(out, item)
		}
	}
	return out
}

// feedItem maps one event into its timeline line. The type drives the glyph and
// the base verb; the parsed payload refines both and supplies the target. The repo
// is always linked on the first line; the target, when present, is the linked
// subject on the second.
func feedItem(e *domain.Event) (view.FeedItemVM, bool) {
	if e.Repo == nil || e.Actor == nil {
		return view.FeedItemVM{}, false
	}
	owner := repoOwner(e.Repo)
	item := view.FeedItemVM{
		ActorLogin:   e.Actor.Login,
		ActorURL:     route.Profile(e.Actor.Login),
		RepoFullName: owner + "/" + e.Repo.Name,
		RepoURL:      route.Repo(owner, e.Repo.Name),
		CreatedAt:    e.CreatedAt.UTC().Format("Jan 2, 2006"),
		CreatedISO:   e.CreatedAt.UTC().Format(time.RFC3339),
	}

	var p feedPayload
	_ = json.Unmarshal(e.Payload, &p)

	switch e.Type {
	case "PushEvent":
		item.Icon = "repo-push"
		item.Verb = "pushed to"
		if branch := strings.TrimPrefix(p.Ref, "refs/heads/"); branch != "" && branch != p.Ref {
			item.Target = branch
			item.TargetURL = route.Tree(owner, e.Repo.Name, branch, "")
		}
	case "IssuesEvent":
		item.Icon = issueIcon(p.Action)
		item.Verb = issueVerb(p.Action)
		applySubject(&item, owner, e.Repo.Name, p.Issue, false)
	case "IssueCommentEvent":
		item.Icon = "comment"
		item.Verb = "commented on an issue in"
		applySubject(&item, owner, e.Repo.Name, p.Issue, false)
	case "PullRequestEvent":
		item.Icon = pullIcon(p.Action)
		item.Verb = pullVerb(p.Action)
		applySubject(&item, owner, e.Repo.Name, p.PullRequest, true)
	case "PullRequestReviewEvent":
		item.Icon = "eye"
		item.Verb = "reviewed a pull request in"
		applySubject(&item, owner, e.Repo.Name, p.PullRequest, true)
	default:
		item.Icon = "dot-fill"
		item.Verb = "had activity in"
	}
	return item, true
}

// applySubject sets the target line from an issue or pull subject, linking it to
// the issue or pull page. A nil or unnumbered subject leaves the line with no
// target, so an event whose payload is not yet rendered still shows its verb and
// repository.
func applySubject(item *view.FeedItemVM, owner, name string, s *feedSubject, isPull bool) {
	if s == nil || s.Number <= 0 {
		return
	}
	label := "#" + strconv.Itoa(s.Number)
	if t := strings.TrimSpace(s.Title); t != "" {
		label += " " + t
	}
	item.Target = label
	if isPull {
		item.TargetURL = route.Pull(owner, name, int64(s.Number))
	} else {
		item.TargetURL = route.Issue(owner, name, int64(s.Number))
	}
}

// issueIcon and issueVerb refine an IssuesEvent by its action; an unknown action
// reads as a neutral update so a future action still renders a sensible line.
func issueIcon(action string) string {
	switch action {
	case "closed":
		return "issue-closed"
	case "reopened":
		return "issue-reopened"
	default:
		return "issue-opened"
	}
}

func issueVerb(action string) string {
	switch action {
	case "closed":
		return "closed an issue in"
	case "reopened":
		return "reopened an issue in"
	case "opened":
		return "opened an issue in"
	default:
		return "updated an issue in"
	}
}

// pullIcon and pullVerb refine a PullRequestEvent by its action the same way.
func pullIcon(action string) string {
	if action == "closed" {
		return "git-pull-request-closed"
	}
	return "git-pull-request"
}

func pullVerb(action string) string {
	switch action {
	case "closed":
		return "closed a pull request in"
	case "reopened":
		return "reopened a pull request in"
	case "opened":
		return "opened a pull request in"
	default:
		return "updated a pull request in"
	}
}
