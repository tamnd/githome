package webhook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/presenter/restmodel"
	"github.com/tamnd/githome/store"
)

// The loaders the renderer assembles an event's objects through. They are the
// no-visibility paths the domain services expose for this purpose: the event was
// already authorized when it was recorded, so the renderer never re-gates it.
type (
	repoLoader interface {
		RepoForEvent(ctx context.Context, repoPK int64) (*domain.Repo, error)
	}
	issueLoader interface {
		IssueForEvent(ctx context.Context, repo *domain.Repo, issuePK int64) (*domain.Issue, error)
	}
	pullLoader interface {
		PullForEvent(ctx context.Context, repo *domain.Repo, issuePK int64) (*domain.PullRequest, error)
	}
	userLoader interface {
		Viewer(ctx context.Context, userPK int64) (*domain.User, error)
	}
)

// Renderer turns a recorded event into the two JSON documents the milestone
// serves: the body POSTed to a hook and the compact payload the Events API
// stores on the event row. It lives here, not in domain, because it imports the
// presenter to reach the exact wire shapes, and domain may not.
type Renderer struct {
	repos  repoLoader
	issues issueLoader
	pulls  pullLoader
	users  userLoader
	urls   *presenter.URLBuilder
	format nodeid.Format
}

// NewRenderer wires a Renderer over the domain loaders and the presenter.
func NewRenderer(repos repoLoader, issues issueLoader, pulls pullLoader, users userLoader, urls *presenter.URLBuilder, format nodeid.Format) *Renderer {
	return &Renderer{repos: repos, issues: issues, pulls: pulls, users: users, urls: urls, format: format}
}

// Rendered is the result of rendering one event: the delivery body, the compact
// feed payload to store, and the header coordinates a delivery carries.
type Rendered struct {
	Event        string // X-GitHub-Event, e.g. "push"
	Action       string // empty for push
	RepositoryID int64  // the event's repository database id
	Body         []byte // the webhook POST body
	Payload      []byte // the Events-API payload object stored on the event
}

// Render assembles an event's objects and renders both documents. push carries
// the moved refs for a push; cd carries ref detail for create/delete events.
// Both are nil for every other event type.
func (r *Renderer) Render(ctx context.Context, ev *store.EventRow, push *domain.PushPayload, cd *domain.CreateDeletePayload) (*Rendered, error) {
	repo, err := r.repos.RepoForEvent(ctx, ev.RepoPK)
	if err != nil {
		return nil, err
	}
	sender, err := r.users.Viewer(ctx, ev.ActorPK)
	if err != nil {
		return nil, err
	}
	var (
		res *Rendered
	)
	switch ev.Event {
	case domain.EventPush:
		res, err = r.renderPush(ev, repo, sender, push)
	case domain.EventIssues, domain.EventIssueComment:
		res, err = r.renderIssue(ctx, ev, repo, sender)
	case domain.EventPullRequest, domain.EventPullRequestReview:
		res, err = r.renderPull(ctx, ev, repo, sender)
	case domain.EventCreate:
		res, err = r.renderCreate(ev, repo, sender, cd)
	case domain.EventDelete:
		res, err = r.renderDelete(ev, repo, sender, cd)
	default:
		return nil, fmt.Errorf("webhook: unknown event %q", ev.Event)
	}
	if err != nil {
		return nil, err
	}
	res.RepositoryID = repo.ID
	return res, nil
}

func (r *Renderer) renderPush(ev *store.EventRow, repo *domain.Repo, sender *domain.User, push *domain.PushPayload) (*Rendered, error) {
	owner := repo.Owner.Login
	var ref, before, after string
	created, deleted := false, false
	if push != nil && len(push.Updates) > 0 {
		u := push.Updates[0]
		ref, before, after = u.Ref, u.OldSHA, u.NewSHA
		created, deleted = u.Created(), u.Deleted()
	}
	body := restmodel.WebhookPush{
		Ref:        ref,
		Before:     before,
		After:      after,
		Created:    created,
		Deleted:    deleted,
		Forced:     false,
		BaseRef:    nil,
		Compare:    r.urls.RepoHTML(owner, repo.Name) + "/compare/" + before + "..." + after,
		Commits:    []any{},
		HeadCommit: nil,
		Repository: r.urls.Repository(repo, r.format, nil),
		Pusher:     restmodel.WebhookPusher{Name: sender.Login, Email: deref(sender.Email)},
		Sender:     r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.PushEventPayload{
		PushID:       ev.DBID,
		Size:         0,
		DistinctSize: 0,
		Ref:          ref,
		Head:         after,
		Before:       before,
		Commits:      []any{},
	}
	return marshalRendered(ev, body, feed)
}

func (r *Renderer) renderIssue(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User) (*Rendered, error) {
	if ev.IssuePK == nil {
		return nil, fmt.Errorf("webhook: %s event has no issue", ev.Event)
	}
	owner := repo.Owner.Login
	iss, err := r.issues.IssueForEvent(ctx, repo, *ev.IssuePK)
	if err != nil {
		return nil, err
	}
	rendered := r.urls.Issue(owner, repo.Name, iss, r.format)
	body := restmodel.WebhookIssues{
		Action:     ev.Action,
		Issue:      rendered,
		Repository: r.urls.Repository(repo, r.format, nil),
		Sender:     r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.IssuesEventPayload{Action: ev.Action, Issue: rendered}
	return marshalRendered(ev, body, feed)
}

func (r *Renderer) renderPull(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User) (*Rendered, error) {
	if ev.IssuePK == nil {
		return nil, fmt.Errorf("webhook: %s event has no pull request", ev.Event)
	}
	owner := repo.Owner.Login
	pr, err := r.pulls.PullForEvent(ctx, repo, *ev.IssuePK)
	if err != nil {
		return nil, err
	}
	rendered := r.urls.PullRequest(owner, repo.Name, pr, r.format, true)
	body := restmodel.WebhookPullRequest{
		Action:      ev.Action,
		Number:      pr.Number,
		PullRequest: rendered,
		Repository:  r.urls.Repository(repo, r.format, nil),
		Sender:      r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.PullRequestEventPayload{Action: ev.Action, Number: pr.Number, PullRequest: rendered}
	return marshalRendered(ev, body, feed)
}

func (r *Renderer) renderCreate(ev *store.EventRow, repo *domain.Repo, sender *domain.User, cd *domain.CreateDeletePayload) (*Rendered, error) {
	var ref, refType, masterBranch string
	if cd != nil {
		ref, refType, masterBranch = cd.Ref, cd.RefType, cd.MasterBranch
	}
	owner := repo.Owner.Login
	body := restmodel.WebhookCreate{
		Ref:          ref,
		RefType:      refType,
		MasterBranch: masterBranch,
		Description:  nil,
		PusherType:   "user",
		Repository:   r.urls.Repository(repo, r.format, nil),
		Sender:       r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.CreateEventPayload{
		Ref:          ref,
		RefType:      refType,
		MasterBranch: masterBranch,
		Description:  "",
	}
	_ = owner
	return marshalRendered(ev, body, feed)
}

func (r *Renderer) renderDelete(ev *store.EventRow, repo *domain.Repo, sender *domain.User, cd *domain.CreateDeletePayload) (*Rendered, error) {
	var ref, refType string
	if cd != nil {
		ref, refType = cd.Ref, cd.RefType
	}
	body := restmodel.WebhookDelete{
		Ref:        ref,
		RefType:    refType,
		PusherType: "user",
		Repository: r.urls.Repository(repo, r.format, nil),
		Sender:     r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.DeleteEventPayload{Ref: ref, RefType: refType}
	return marshalRendered(ev, body, feed)
}

// marshalRendered encodes the body and feed payload and stamps the header
// coordinates onto the result.
func marshalRendered(ev *store.EventRow, body, feed any) (*Rendered, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	feedJSON, err := json.Marshal(feed)
	if err != nil {
		return nil, err
	}
	return &Rendered{Event: ev.Event, Action: ev.Action, Body: bodyJSON, Payload: feedJSON}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
