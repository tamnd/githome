package domain

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/tamnd/githome/store"
)

// NotificationStore is the slice of the store the notification service needs:
// the thread table itself plus the lookups that drive fan-out and assembly.
type NotificationStore interface {
	UpsertNotificationThread(ctx context.Context, r *store.NotificationThreadRow) error
	ListNotificationThreads(ctx context.Context, userPK, repoPK int64, all bool, limit, offset int) ([]*store.NotificationThreadRow, int, error)
	NotificationThreadByPK(ctx context.Context, pk int64) (*store.NotificationThreadRow, error)
	MarkNotificationThreadRead(ctx context.Context, pk int64) error
	MarkNotificationThreadsRead(ctx context.Context, userPK, repoPK int64) error
	SetNotificationThreadSubscription(ctx context.Context, pk int64, subscribed, ignored bool) error
	DeleteNotificationThread(ctx context.Context, pk int64) error

	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	GetIssueByNumber(ctx context.Context, repoPK, number int64) (*store.IssueRow, error)
	GetIssueByPK(ctx context.Context, pk int64) (*store.IssueRow, error)
	ListAssigneePKs(ctx context.Context, issuePK int64) ([]int64, error)
	ListIssueComments(ctx context.Context, issuePK int64, limit, offset int) ([]store.CommentRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
}

// NotificationThread is one user's view of one issue or pull request
// conversation, the unit the notifications API serves.
type NotificationThread struct {
	ID            int64
	Reason        string
	Unread        bool
	Subscribed    bool
	Ignored       bool
	UpdatedAt     time.Time
	CreatedAt     time.Time
	LastReadAt    *time.Time
	RepoPK        int64
	SubjectTitle  string
	SubjectNumber int64
	SubjectIsPull bool
}

// NotificationService maintains and serves notification threads. Fan-out is
// deliberately small: it covers the events the server already handles inline
// (comments, issue and pull creation, assignment, review requests) and never
// fails the write that triggered it.
type NotificationService struct {
	store NotificationStore
}

// NewNotificationService builds a NotificationService over the store.
func NewNotificationService(st NotificationStore) *NotificationService {
	return &NotificationService{store: st}
}

// List returns one page of the user's threads plus the total for the filter.
// A zero repoPK spans all repositories; all=false keeps only unread threads.
func (s *NotificationService) List(ctx context.Context, userPK, repoPK int64, all bool, page, perPage int) ([]*NotificationThread, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 30
	}
	rows, total, err := s.store.ListNotificationThreads(ctx, userPK, repoPK, all, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*NotificationThread, 0, len(rows))
	for _, r := range rows {
		t, err := s.assemble(ctx, r)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, nil
}

// Get returns one thread. A thread that does not exist or belongs to another
// user is the same ErrNotFound, so thread ids cannot be probed.
func (s *NotificationService) Get(ctx context.Context, userPK, id int64) (*NotificationThread, error) {
	r, err := s.load(ctx, userPK, id)
	if err != nil {
		return nil, err
	}
	return s.assemble(ctx, r)
}

// MarkRead marks one of the user's threads read.
func (s *NotificationService) MarkRead(ctx context.Context, userPK, id int64) error {
	r, err := s.load(ctx, userPK, id)
	if err != nil {
		return err
	}
	return s.store.MarkNotificationThreadRead(ctx, r.PK)
}

// MarkAllRead marks every thread of the user read, optionally scoped to one
// repository by its pk (zero spans all).
func (s *NotificationService) MarkAllRead(ctx context.Context, userPK, repoPK int64) error {
	return s.store.MarkNotificationThreadsRead(ctx, userPK, repoPK)
}

// MarkDone removes a thread from the user's inbox entirely.
func (s *NotificationService) MarkDone(ctx context.Context, userPK, id int64) error {
	r, err := s.load(ctx, userPK, id)
	if err != nil {
		return err
	}
	return s.store.DeleteNotificationThread(ctx, r.PK)
}

// SetSubscription updates the thread's subscription flags and returns the
// thread as updated.
func (s *NotificationService) SetSubscription(ctx context.Context, userPK, id int64, subscribed, ignored bool) (*NotificationThread, error) {
	r, err := s.load(ctx, userPK, id)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetNotificationThreadSubscription(ctx, r.PK, subscribed, ignored); err != nil {
		return nil, err
	}
	return s.Get(ctx, userPK, id)
}

// DeleteSubscription resets the thread to its default subscription state.
func (s *NotificationService) DeleteSubscription(ctx context.Context, userPK, id int64) error {
	r, err := s.load(ctx, userPK, id)
	if err != nil {
		return err
	}
	return s.store.SetNotificationThreadSubscription(ctx, r.PK, true, false)
}

// RepoPKByName resolves a repository pk for the repo-scoped notification
// endpoints. Visibility is the caller's concern.
func (s *NotificationService) RepoPKByName(ctx context.Context, owner, name string) (int64, error) {
	repo, err := s.store.RepoByOwnerName(ctx, owner, name)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	return repo.PK, nil
}

// NotifyIssueComment fans a new comment out to the thread's participants: the
// issue author, its assignees, the authors of earlier comments, and anyone
// @mentioned in the body. The commenter never notifies themselves.
func (s *NotificationService) NotifyIssueComment(ctx context.Context, actorPK int64, owner, repo string, number int64, body string) {
	iss, repoPK, ok := s.lookupIssue(ctx, owner, repo, number)
	if !ok {
		return
	}
	recipients := map[int64]string{}
	if comments, err := s.store.ListIssueComments(ctx, iss.PK, 100, 0); err == nil {
		for _, cm := range comments {
			recipients[cm.UserPK] = "comment"
		}
	}
	recipients[iss.UserPK] = "author"
	if pks, err := s.store.ListAssigneePKs(ctx, iss.PK); err == nil {
		for _, pk := range pks {
			recipients[pk] = "assign"
		}
	}
	for _, pk := range s.mentionPKs(ctx, body) {
		recipients[pk] = "mention"
	}
	s.deliver(ctx, actorPK, repoPK, iss.PK, recipients)
}

// NotifyIssueOpened fans a new issue or pull request out to its assignees and
// to anyone @mentioned in the opening body.
func (s *NotificationService) NotifyIssueOpened(ctx context.Context, actorPK int64, owner, repo string, number int64, body string) {
	iss, repoPK, ok := s.lookupIssue(ctx, owner, repo, number)
	if !ok {
		return
	}
	recipients := map[int64]string{}
	if pks, err := s.store.ListAssigneePKs(ctx, iss.PK); err == nil {
		for _, pk := range pks {
			recipients[pk] = "assign"
		}
	}
	for _, pk := range s.mentionPKs(ctx, body) {
		recipients[pk] = "mention"
	}
	s.deliver(ctx, actorPK, repoPK, iss.PK, recipients)
}

// NotifyAssigned tells the named users they were assigned to the issue.
func (s *NotificationService) NotifyAssigned(ctx context.Context, actorPK int64, owner, repo string, number int64, logins []string) {
	s.notifyLogins(ctx, actorPK, owner, repo, number, logins, "assign")
}

// NotifyReviewRequested tells the named users their review was requested.
func (s *NotificationService) NotifyReviewRequested(ctx context.Context, actorPK int64, owner, repo string, number int64, logins []string) {
	s.notifyLogins(ctx, actorPK, owner, repo, number, logins, "review_requested")
}

func (s *NotificationService) notifyLogins(ctx context.Context, actorPK int64, owner, repo string, number int64, logins []string, reason string) {
	if len(logins) == 0 {
		return
	}
	iss, repoPK, ok := s.lookupIssue(ctx, owner, repo, number)
	if !ok {
		return
	}
	recipients := map[int64]string{}
	for _, login := range logins {
		if u, err := s.store.UserByLogin(ctx, login); err == nil {
			recipients[u.PK] = reason
		}
	}
	s.deliver(ctx, actorPK, repoPK, iss.PK, recipients)
}

// load fetches a thread and checks it belongs to the user.
func (s *NotificationService) load(ctx context.Context, userPK, id int64) (*store.NotificationThreadRow, error) {
	r, err := s.store.NotificationThreadByPK(ctx, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if r.UserPK != userPK {
		return nil, ErrNotFound
	}
	return r, nil
}

// assemble joins a thread row with its subject issue.
func (s *NotificationService) assemble(ctx context.Context, r *store.NotificationThreadRow) (*NotificationThread, error) {
	t := &NotificationThread{
		ID:         r.PK,
		Reason:     r.Reason,
		Unread:     r.Unread,
		Subscribed: r.Subscribed,
		Ignored:    r.Ignored,
		UpdatedAt:  r.UpdatedAt,
		CreatedAt:  r.CreatedAt,
		LastReadAt: r.LastReadAt,
		RepoPK:     r.RepoPK,
	}
	iss, err := s.store.GetIssueByPK(ctx, r.IssuePK)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	t.SubjectTitle = iss.Title
	t.SubjectNumber = iss.Number
	t.SubjectIsPull = iss.IsPull
	return t, nil
}

func (s *NotificationService) lookupIssue(ctx context.Context, owner, repo string, number int64) (*store.IssueRow, int64, bool) {
	rr, err := s.store.RepoByOwnerName(ctx, owner, repo)
	if err != nil {
		return nil, 0, false
	}
	iss, err := s.store.GetIssueByNumber(ctx, rr.PK, number)
	if err != nil {
		return nil, 0, false
	}
	return iss, rr.PK, true
}

// deliver upserts a thread per recipient, skipping the actor. Errors are
// dropped on purpose: a failed notification must not fail the write behind it.
func (s *NotificationService) deliver(ctx context.Context, actorPK, repoPK, issuePK int64, recipients map[int64]string) {
	for pk, reason := range recipients {
		if pk == actorPK || pk == 0 {
			continue
		}
		_ = s.store.UpsertNotificationThread(ctx, &store.NotificationThreadRow{
			UserPK:  pk,
			RepoPK:  repoPK,
			IssuePK: issuePK,
			Reason:  reason,
		})
	}
}

// mapStoreErr folds the store's not-found into the domain's, leaving every
// other error untouched.
func mapStoreErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// mentionRe matches GitHub-style @login mentions: up to 39 characters of
// alphanumerics and single hyphens, never starting with a hyphen.
var mentionRe = regexp.MustCompile(`@([a-zA-Z0-9](?:-?[a-zA-Z0-9]){0,38})`)

// mentionPKs resolves the @mentions in a body to user pks, dropping anything
// that is not an existing login.
func (s *NotificationService) mentionPKs(ctx context.Context, body string) []int64 {
	var out []int64
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(body, -1) {
		login := m[1]
		if seen[login] {
			continue
		}
		seen[login] = true
		if u, err := s.store.UserByLogin(ctx, login); err == nil {
			out = append(out, u.PK)
		}
	}
	return out
}
