package webhook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
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
		CommentForEvent(ctx context.Context, commentPK int64) (*domain.Comment, error)
	}
	pullLoader interface {
		PullForEvent(ctx context.Context, repo *domain.Repo, issuePK int64) (*domain.PullRequest, error)
	}
	userLoader interface {
		Viewer(ctx context.Context, userPK int64) (*domain.User, error)
	}
	// reviewLoader is the slice of the review service the review and inline
	// comment bodies render through. It is optional, bound via BindReviews;
	// without it a review event fails to render, which only happens in a wiring
	// that records review events but never binds the loader.
	reviewLoader interface {
		ReviewForEvent(ctx context.Context, reviewPK int64) (*domain.Review, error)
		ReviewCommentForEvent(ctx context.Context, commentPK int64) (*domain.ReviewComment, error)
	}
	// releaseLoader is the slice of the release service a release body renders
	// through. Optional like the review loader, bound via BindReleases.
	releaseLoader interface {
		ReleaseForEvent(ctx context.Context, releasePK int64) (*domain.Release, error)
	}
	// gitLoader is the slice of the git layer a push body renders through. It
	// is optional, bound via BindGit, so a renderer without a git store still
	// renders every event; a push then carries empty commit lists, the shape
	// this package served before the walk existed.
	gitLoader interface {
		PushCommits(ctx context.Context, pk int64, before, after string, limit int) ([]git.Commit, error)
		CommitFiles(ctx context.Context, pk int64, sha string) (added, removed, modified []string, err error)
		IsAncestor(ctx context.Context, pk int64, ancestor, descendant string) (bool, error)
		RefSHA(ctx context.Context, pk int64, ref string) (string, error)
	}
)

// Renderer turns a recorded event into the two JSON documents the milestone
// serves: the body POSTed to a hook and the compact payload the Events API
// stores on the event row. It lives here, not in domain, because it imports the
// presenter to reach the exact wire shapes, and domain may not.
type Renderer struct {
	repos    repoLoader
	issues   issueLoader
	pulls    pullLoader
	users    userLoader
	urls     *presenter.URLBuilder
	format   nodeid.Format
	git      gitLoader
	reviews  reviewLoader
	releases releaseLoader
}

// NewRenderer wires a Renderer over the domain loaders and the presenter.
func NewRenderer(repos repoLoader, issues issueLoader, pulls pullLoader, users userLoader, urls *presenter.URLBuilder, format nodeid.Format) *Renderer {
	return &Renderer{repos: repos, issues: issues, pulls: pulls, users: users, urls: urls, format: format}
}

// BindGit attaches the git layer the push renderer walks the pushed range
// through. Without it a push body has empty commits and a null head_commit.
func (r *Renderer) BindGit(g gitLoader) { r.git = g }

// BindReviews attaches the review loader the pull_request_review and
// pull_request_review_comment bodies render their review and comment through.
func (r *Renderer) BindReviews(rl reviewLoader) { r.reviews = rl }

// BindReleases attaches the loader a release body renders its release through.
func (r *Renderer) BindReleases(rl releaseLoader) { r.releases = rl }

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
// the moved refs for a push; cd carries ref detail for create/delete events;
// detail carries the secondary coordinates some bodies embed, like the moved
// head shas a pull_request synchronize reports. Each is nil when the event type
// has no use for it.
func (r *Renderer) Render(ctx context.Context, ev *store.EventRow, push *domain.PushPayload, cd *domain.CreateDeletePayload, detail *domain.EventDetail) (*Rendered, error) {
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
		res, err = r.renderPush(ctx, ev, repo, sender, push)
	case domain.EventIssues:
		res, err = r.renderIssue(ctx, ev, repo, sender, detail)
	case domain.EventIssueComment:
		res, err = r.renderIssueComment(ctx, ev, repo, sender, detail)
	case domain.EventPullRequest:
		res, err = r.renderPull(ctx, ev, repo, sender, detail)
	case domain.EventPullRequestReview:
		res, err = r.renderPullReview(ctx, ev, repo, sender, detail)
	case domain.EventPullRequestReviewComment:
		res, err = r.renderReviewComment(ctx, ev, repo, sender, detail)
	case domain.EventRelease:
		res, err = r.renderRelease(ctx, ev, repo, sender, detail)
	case domain.EventCreate:
		res, err = r.renderCreate(ev, repo, sender, cd)
	case domain.EventDelete:
		res, err = r.renderDelete(ev, repo, sender, cd)
	case domain.EventRepositoryDispatch:
		res, err = r.renderRepositoryDispatch(ev, repo, sender, detail)
	default:
		return nil, fmt.Errorf("webhook: unknown event %q", ev.Event)
	}
	if err != nil {
		return nil, err
	}
	res.RepositoryID = repo.ID
	return res, nil
}

// zenLines is the pool a ping body's zen line is drawn from, the small fixed
// list standing in for GitHub's.
var zenLines = []string{
	"Keep it logically awesome.",
	"Practicality beats purity.",
	"Design for failure.",
	"Responsive is better than fast.",
	"Speak like a human.",
	"Mind your words, they are important.",
}

// RenderPing builds the body of a ping delivery: {zen, hook_id, hook} plus the
// repository and sender every delivery carries. A ping has no event row, so it
// renders straight from the hook.
func (r *Renderer) RenderPing(ctx context.Context, row *store.WebhookRow, senderPK int64) (*Rendered, error) {
	repo, err := r.repos.RepoForEvent(ctx, row.RepoPK)
	if err != nil {
		return nil, err
	}
	sender, err := r.users.Viewer(ctx, senderPK)
	if err != nil {
		return nil, err
	}
	hook := domain.HookForDelivery(row)
	body := restmodel.WebhookPing{
		Zen:    zenLines[int(row.DBID)%len(zenLines)],
		HookID: hook.ID,
		Sender: r.urls.SimpleUser(sender, r.format),
	}
	if repo.Name == domain.OrgHookRepo {
		// An org hook anchors on the synthetic repo; its ping renders the hook
		// in the /orgs URL space and carries no repository.
		body.Hook = r.urls.OrgHook(repo.Owner.Login, hook)
	} else {
		body.Hook = r.urls.Hook(repo.Owner.Login, repo.Name, hook)
		repository := r.urls.Repository(repo, r.format, nil)
		body.Repository = &repository
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &Rendered{Event: "ping", RepositoryID: repo.ID, Body: bodyJSON}, nil
}

// pushCommitLimit caps the commits a push body carries, matching GitHub: the
// newest twenty, listed oldest first, with head_commit always the new tip.
const pushCommitLimit = 20

func (r *Renderer) renderPush(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User, push *domain.PushPayload) (*Rendered, error) {
	owner := repo.Owner.Login
	var ref, before, after string
	created, deleted := false, false
	if push != nil && len(push.Updates) > 0 {
		u := push.Updates[0]
		ref, before, after = u.Ref, u.OldSHA, u.NewSHA
		created, deleted = u.Created(), u.Deleted()
	}
	commits := []restmodel.WebhookCommit{}
	var head *restmodel.WebhookCommit
	forced := false
	var baseRef *string
	// The walk is best effort: a render must never fail because the repository
	// on disk is missing or a sha is gone, so a walk error just leaves the
	// commit list empty while the moved tips still go out.
	if r.git != nil && !deleted && after != "" {
		var list []git.Commit
		if created {
			list, baseRef = r.walkCreatedRef(ctx, repo, ref, after)
		} else {
			if ok, err := r.git.IsAncestor(ctx, repo.PK, before, after); err == nil {
				forced = !ok
			}
			list, _ = r.git.PushCommits(ctx, repo.PK, before, after, pushCommitLimit)
		}
		commits = r.webhookCommits(ctx, repo, sender, list)
		if len(commits) > 0 {
			head = &commits[len(commits)-1]
		}
	}
	body := restmodel.WebhookPush{
		Ref:        ref,
		Before:     before,
		After:      after,
		Created:    created,
		Deleted:    deleted,
		Forced:     forced,
		BaseRef:    baseRef,
		Compare:    r.urls.RepoHTML(owner, repo.Name) + "/compare/" + before + "..." + after,
		Commits:    commits,
		HeadCommit: head,
		Repository: r.urls.Repository(repo, r.format, nil),
		Pusher:     restmodel.WebhookPusher{Name: sender.Login, Email: deref(sender.Email)},
		Sender:     r.urls.SimpleUser(sender, r.format),
	}
	feedCommits := make([]restmodel.PushEventCommit, 0, len(commits))
	for i := range commits {
		c := &commits[i]
		feedCommits = append(feedCommits, restmodel.PushEventCommit{
			SHA:      c.ID,
			Author:   restmodel.PushEventCommitIdent{Email: c.Author.Email, Name: c.Author.Name},
			Message:  c.Message,
			Distinct: c.Distinct,
			URL:      r.urls.API("repos", owner, repo.Name, "commits", c.ID),
		})
	}
	feed := restmodel.PushEventPayload{
		PushID:       ev.DBID,
		Size:         len(commits),
		DistinctSize: len(commits),
		Ref:          ref,
		Head:         after,
		Before:       before,
		Commits:      feedCommits,
	}
	return marshalRendered(ev, body, feed)
}

// walkCreatedRef walks the commits a new ref introduced. When the new ref's tip
// is reachable from another branch the push carries no new commits and GitHub
// reports the ref it was cut from as base_ref; the default branch is the one
// candidate Githome checks. Otherwise the range is bounded by the default
// branch tip so only the new work is listed.
func (r *Renderer) walkCreatedRef(ctx context.Context, repo *domain.Repo, ref, after string) ([]git.Commit, *string) {
	defRef := "refs/heads/" + repo.DefaultBranch
	if ref != defRef && repo.DefaultBranch != "" {
		if tip, err := r.git.RefSHA(ctx, repo.PK, defRef); err == nil && tip != "" {
			var baseRef *string
			if ok, err := r.git.IsAncestor(ctx, repo.PK, after, tip); err == nil && ok {
				baseRef = &defRef
			}
			list, _ := r.git.PushCommits(ctx, repo.PK, tip, after, pushCommitLimit)
			return list, baseRef
		}
	}
	list, _ := r.git.PushCommits(ctx, repo.PK, "", after, pushCommitLimit)
	return list, nil
}

// webhookCommits maps walked commits onto the wire shape, attaching each
// commit's file lists. username is filled in only when the commit identity
// matches the pusher, the one account in hand; other identities go out as bare
// git identities.
func (r *Renderer) webhookCommits(ctx context.Context, repo *domain.Repo, sender *domain.User, list []git.Commit) []restmodel.WebhookCommit {
	out := make([]restmodel.WebhookCommit, 0, len(list))
	for i := range list {
		c := &list[i]
		wc := restmodel.WebhookCommit{
			ID:        c.SHA,
			TreeID:    c.Tree,
			Distinct:  true,
			Message:   c.Message,
			Timestamp: restmodel.NewTime(c.Author.When),
			URL:       r.urls.RepoHTML(repo.Owner.Login, repo.Name) + "/commit/" + c.SHA,
			Author:    r.commitUser(c.Author, sender),
			Committer: r.commitUser(c.Committer, sender),
			Added:     []string{},
			Removed:   []string{},
			Modified:  []string{},
		}
		if added, removed, modified, err := r.git.CommitFiles(ctx, repo.PK, c.SHA); err == nil {
			wc.Added, wc.Removed, wc.Modified = added, removed, modified
		}
		out = append(out, wc)
	}
	return out
}

// commitUser maps a git signature onto the wire identity, naming the account
// when the email is the sender's.
func (r *Renderer) commitUser(sig git.Signature, sender *domain.User) restmodel.WebhookCommitUser {
	u := restmodel.WebhookCommitUser{Name: sig.Name, Email: sig.Email}
	if sender != nil && sender.Email != nil && *sender.Email != "" && sig.Email == *sender.Email {
		u.Username = sender.Login
	}
	return u
}

func (r *Renderer) renderIssue(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User, detail *domain.EventDetail) (*Rendered, error) {
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
		Label:      labelByName(rendered.Labels, detail),
		Repository: r.urls.Repository(repo, r.format, nil),
		Sender:     r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.IssuesEventPayload{Action: ev.Action, Issue: rendered}
	return marshalRendered(ev, body, feed)
}

// labelByName picks the named label out of the rendered issue's label list, the
// object a labeled delivery embeds. Nil when the event carries no label name or
// the label is gone by render time.
func labelByName(labels []restmodel.Label, detail *domain.EventDetail) *restmodel.Label {
	if detail == nil || detail.Label == "" {
		return nil
	}
	for i := range labels {
		if labels[i].Name == detail.Label {
			return &labels[i]
		}
	}
	return nil
}

func (r *Renderer) renderPull(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User, detail *domain.EventDetail) (*Rendered, error) {
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
		Label:       labelByName(rendered.Labels, detail),
		Repository:  r.urls.Repository(repo, r.format, nil),
		Sender:      r.urls.SimpleUser(sender, r.format),
	}
	if detail != nil {
		body.Before, body.After = detail.Before, detail.After
	}
	feed := restmodel.PullRequestEventPayload{Action: ev.Action, Number: pr.Number, PullRequest: rendered}
	return marshalRendered(ev, body, feed)
}

// renderIssueComment builds an issue_comment body: the comment object the
// detail names alongside the issue it landed on.
func (r *Renderer) renderIssueComment(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User, detail *domain.EventDetail) (*Rendered, error) {
	if ev.IssuePK == nil {
		return nil, fmt.Errorf("webhook: %s event has no issue", ev.Event)
	}
	if detail == nil || detail.CommentPK == 0 {
		return nil, fmt.Errorf("webhook: %s event has no comment", ev.Event)
	}
	owner := repo.Owner.Login
	iss, err := r.issues.IssueForEvent(ctx, repo, *ev.IssuePK)
	if err != nil {
		return nil, err
	}
	cm, err := r.issues.CommentForEvent(ctx, detail.CommentPK)
	if err != nil {
		return nil, err
	}
	renderedIssue := r.urls.Issue(owner, repo.Name, iss, r.format)
	renderedComment := r.urls.IssueComment(owner, repo.Name, cm, r.format)
	body := restmodel.WebhookIssueComment{
		Action:     ev.Action,
		Issue:      renderedIssue,
		Comment:    renderedComment,
		Repository: r.urls.Repository(repo, r.format, nil),
		Sender:     r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.IssueCommentEventPayload{Action: ev.Action, Issue: renderedIssue, Comment: renderedComment}
	return marshalRendered(ev, body, feed)
}

// renderPullReview builds a pull_request_review body: the review the detail
// names alongside its pull request.
func (r *Renderer) renderPullReview(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User, detail *domain.EventDetail) (*Rendered, error) {
	if ev.IssuePK == nil {
		return nil, fmt.Errorf("webhook: %s event has no pull request", ev.Event)
	}
	if r.reviews == nil || detail == nil || detail.ReviewPK == 0 {
		return nil, fmt.Errorf("webhook: %s event has no review", ev.Event)
	}
	owner := repo.Owner.Login
	pr, err := r.pulls.PullForEvent(ctx, repo, *ev.IssuePK)
	if err != nil {
		return nil, err
	}
	rev, err := r.reviews.ReviewForEvent(ctx, detail.ReviewPK)
	if err != nil {
		return nil, err
	}
	renderedPull := r.urls.PullRequest(owner, repo.Name, pr, r.format, true)
	renderedReview := r.urls.Review(owner, repo.Name, rev, r.format)
	body := restmodel.WebhookPullRequestReview{
		Action:      ev.Action,
		Review:      renderedReview,
		PullRequest: renderedPull,
		Repository:  r.urls.Repository(repo, r.format, nil),
		Sender:      r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.PullRequestReviewEventPayload{Action: ev.Action, Review: renderedReview, PullRequest: renderedPull}
	return marshalRendered(ev, body, feed)
}

// renderReviewComment builds a pull_request_review_comment body: the inline
// comment the detail names alongside its pull request.
func (r *Renderer) renderReviewComment(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User, detail *domain.EventDetail) (*Rendered, error) {
	if ev.IssuePK == nil {
		return nil, fmt.Errorf("webhook: %s event has no pull request", ev.Event)
	}
	if r.reviews == nil || detail == nil || detail.ReviewCommentPK == 0 {
		return nil, fmt.Errorf("webhook: %s event has no review comment", ev.Event)
	}
	owner := repo.Owner.Login
	pr, err := r.pulls.PullForEvent(ctx, repo, *ev.IssuePK)
	if err != nil {
		return nil, err
	}
	cm, err := r.reviews.ReviewCommentForEvent(ctx, detail.ReviewCommentPK)
	if err != nil {
		return nil, err
	}
	renderedPull := r.urls.PullRequest(owner, repo.Name, pr, r.format, true)
	renderedComment := r.urls.ReviewComment(owner, repo.Name, cm, r.format)
	body := restmodel.WebhookPullRequestReviewComment{
		Action:      ev.Action,
		Comment:     renderedComment,
		PullRequest: renderedPull,
		Repository:  r.urls.Repository(repo, r.format, nil),
		Sender:      r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.PullRequestReviewCommentEventPayload{Action: ev.Action, Comment: renderedComment, PullRequest: renderedPull}
	return marshalRendered(ev, body, feed)
}

// renderRelease builds a release body: the release object the detail names.
func (r *Renderer) renderRelease(ctx context.Context, ev *store.EventRow, repo *domain.Repo, sender *domain.User, detail *domain.EventDetail) (*Rendered, error) {
	if r.releases == nil || detail == nil || detail.ReleasePK == 0 {
		return nil, fmt.Errorf("webhook: %s event has no release", ev.Event)
	}
	rel, err := r.releases.ReleaseForEvent(ctx, detail.ReleasePK)
	if err != nil {
		return nil, err
	}
	rendered := r.urls.Release(repo.Owner.Login, repo.Name, rel, r.format)
	body := restmodel.WebhookRelease{
		Action:     ev.Action,
		Release:    rendered,
		Repository: r.urls.Repository(repo, r.format, nil),
		Sender:     r.urls.SimpleUser(sender, r.format),
	}
	feed := restmodel.ReleaseEventPayload{Action: ev.Action, Release: rendered}
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

// renderRepositoryDispatch builds the repository_dispatch delivery: its action
// is the caller's event_type and it carries the opaque client_payload verbatim,
// plus the default branch and the usual repository and sender objects. The
// event has no public Events-API timeline entry, so the stored feed payload
// mirrors the delivery body.
func (r *Renderer) renderRepositoryDispatch(ev *store.EventRow, repo *domain.Repo, sender *domain.User, detail *domain.EventDetail) (*Rendered, error) {
	action, clientPayload := "", json.RawMessage("{}")
	if detail != nil {
		action = detail.Action
		if len(detail.ClientPayload) > 0 {
			clientPayload = detail.ClientPayload
		}
	}
	body := map[string]any{
		"action":         action,
		"branch":         repo.DefaultBranch,
		"client_payload": clientPayload,
		"repository":     r.urls.Repository(repo, r.format, nil),
		"sender":         r.urls.SimpleUser(sender, r.format),
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &Rendered{Event: ev.Event, Action: action, Body: bodyJSON, Payload: bodyJSON}, nil
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
