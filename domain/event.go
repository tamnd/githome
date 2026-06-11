package domain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// Webhook and Events API event names. These are the GitHub event identifiers the
// X-GitHub-Event delivery header and the Events API "type" both derive from.
const (
	EventPush                     = "push"
	EventIssues                   = "issues"
	EventIssueComment             = "issue_comment"
	EventPullRequest              = "pull_request"
	EventPullRequestReview        = "pull_request_review"
	EventPullRequestReviewComment = "pull_request_review_comment"
	EventRelease                  = "release"
	EventCreate                   = "create" // branch or tag created via REST or push
	EventDelete                   = "delete" // branch or tag deleted via REST or push
)

// The fan-out job kinds the webhook milestone introduces. deliver_event loads
// one recorded event, renders its payload, and enqueues a delivery per
// subscribed hook; deliver_webhook performs one POST and records the result. The
// handlers live in the webhook package, which may import domain and presenter as
// a leaf consumer; defining the kinds here keeps the producer and the constants
// together the same way the push and recompute kinds are.
const (
	JobDeliverEvent   = "deliver_event"
	JobDeliverWebhook = "deliver_webhook"
)

// DeliverEventPayload is the body of a deliver_event job: the recorded event to
// fan out, plus event-type-specific detail that has no home in a table and so
// rides along.
type DeliverEventPayload struct {
	EventPK      int64                `json:"event_pk"`
	Push         *PushPayload         `json:"push,omitempty"`
	CreateDelete *CreateDeletePayload `json:"create_delete,omitempty"`
	Detail       *EventDetail         `json:"detail,omitempty"`
}

// EventDetail pins the secondary coordinates some events render from and that
// have no column on the event row: the comment, review, or release the body
// embeds, the label a labeled action names, and the moved head shas a
// pull_request synchronize reports at the top level as before/after.
type EventDetail struct {
	CommentPK       int64  `json:"comment_pk,omitempty"`
	ReviewPK        int64  `json:"review_pk,omitempty"`
	ReviewCommentPK int64  `json:"review_comment_pk,omitempty"`
	ReleasePK       int64  `json:"release_pk,omitempty"`
	Label           string `json:"label,omitempty"`
	Before          string `json:"before,omitempty"`
	After           string `json:"after,omitempty"`
}

// CreateDeletePayload carries the ref detail for create and delete webhook
// events. RefType is "branch" or "tag"; MasterBranch is only meaningful on
// create events.
type CreateDeletePayload struct {
	Ref          string `json:"ref"`
	RefType      string `json:"ref_type"` // "branch" or "tag"
	MasterBranch string `json:"master_branch,omitempty"`
}

// PushPayload is the parsed push a deliver_event job carries so the renderer can
// build the push webhook body and the PushEvent feed entry from the moved refs.
type PushPayload struct {
	RepoPK   int64       `json:"repo_pk"`
	PusherPK int64       `json:"pusher_pk"`
	Protocol string      `json:"protocol"`
	Updates  []RefUpdate `json:"updates"`
}

// DeliverWebhookPayload is the body of a deliver_webhook job: the hook to POST
// to and the event whose body to render and send. Push carries the moved refs a
// push event has no table to reload from, propagated from the deliver_event job
// so each hook's body renders the same push. CreateDelete carries ref detail
// for create/delete events. RedeliverOf, when set, replays a recorded delivery
// instead of rendering the event afresh. Ping, when set, sends the ping body
// instead of an event: there is no event row, so EventPK is zero and SenderPK
// names the actor who triggered it.
type DeliverWebhookPayload struct {
	WebhookPK    int64                `json:"webhook_pk"`
	EventPK      int64                `json:"event_pk"`
	Push         *PushPayload         `json:"push,omitempty"`
	CreateDelete *CreateDeletePayload `json:"create_delete,omitempty"`
	Detail       *EventDetail         `json:"detail,omitempty"`
	RedeliverOf  int64                `json:"redeliver_of,omitempty"`
	Ping         bool                 `json:"ping,omitempty"`
	SenderPK     int64                `json:"sender_pk,omitempty"`
}

// eventRecorder is the slice of the store the event sink writes through: one
// append of an event row. The concrete store satisfies it, so the mutating
// services reach it through the same interface they already hold.
type eventRecorder interface {
	InsertEvent(ctx context.Context, e *store.EventRow) error
}

// batchEventRecorder is an optional extension of eventRecorder: when the
// concrete store implements it, recordEvent uses InsertEventAndJob to combine
// the event append and the fan-out job enqueue into one transaction, cutting the
// post-mutation write-transaction count from two to one.
type batchEventRecorder interface {
	eventRecorder
	InsertEventAndJob(ctx context.Context, e *store.EventRow, jobKind string, buildPayload func(int64) string) error
}

// recordEvent appends an event row and enqueues its fan-out job. Delivery is
// best-effort: a failure to record or enqueue never fails the user's write,
// matching how GitHub detaches webhook delivery from the API call that triggered
// it. push is nil for every event except a push, where it carries the moved refs
// the renderer needs.
//
// When st also implements batchEventRecorder the event append and the deliver_event
// job insert land in one transaction; otherwise they fall back to two separate
// round trips.
func recordEvent(ctx context.Context, st eventRecorder, enq worker.Enqueuer, ev *store.EventRow, push *PushPayload) {
	recordEventFull(ctx, st, enq, ev, push, nil, nil)
}

func recordEventFull(ctx context.Context, st eventRecorder, enq worker.Enqueuer, ev *store.EventRow, push *PushPayload, cd *CreateDeletePayload, detail *EventDetail) {
	if batcher, ok := st.(batchEventRecorder); ok {
		_ = batcher.InsertEventAndJob(ctx, ev, JobDeliverEvent, func(eventPK int64) string {
			p, _ := json.Marshal(DeliverEventPayload{EventPK: eventPK, Push: push, CreateDelete: cd, Detail: detail})
			return string(p)
		})
		return
	}
	if err := st.InsertEvent(ctx, ev); err != nil {
		return
	}
	payload, err := json.Marshal(DeliverEventPayload{EventPK: ev.PK, Push: push, CreateDelete: cd, Detail: detail})
	if err != nil {
		return
	}
	_, _ = enq.Enqueue(ctx, JobDeliverEvent, string(payload), "")
}

// EventStore is the slice of the store the activity feed reads through: the
// scoped event list, the login lookup the per-user feed resolves an actor by,
// and the by-pk lookups that resolve each event's actor and repository into the
// compact objects the feed embeds.
type EventStore interface {
	ListEvents(ctx context.Context, f store.EventFilter) ([]store.EventRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
}

// Event is the domain view of one activity-feed entry: the resolved actor and
// repository, the GitHub event type, and the payload object the fan-out worker
// rendered and stored. Payload is the raw JSON the feed serves verbatim.
type Event struct {
	ID        int64
	Type      string
	Actor     *User
	Repo      *Repo
	Payload   json.RawMessage
	Public    bool
	CreatedAt time.Time
}

// eventType maps an internal event name to the Events-API type string.
func eventType(name string) string {
	switch name {
	case EventPush:
		return "PushEvent"
	case EventIssues:
		return "IssuesEvent"
	case EventIssueComment:
		return "IssueCommentEvent"
	case EventPullRequest:
		return "PullRequestEvent"
	case EventPullRequestReview:
		return "PullRequestReviewEvent"
	case EventPullRequestReviewComment:
		return "PullRequestReviewCommentEvent"
	case EventRelease:
		return "ReleaseEvent"
	case EventCreate:
		return "CreateEvent"
	case EventDelete:
		return "DeleteEvent"
	default:
		return name
	}
}

// EventService serves the pull-based activity feed: the global public timeline,
// a repository's timeline (gated by the same visibility rule as every other repo
// read), and a user's timeline. It reads the rendered payload the fan-out worker
// stored on each event, so the feed never re-derives what a delivery already
// built.
type EventService struct {
	store EventStore
	repos *RepoService
}

// NewEventService builds an EventService over the store and the repo service.
func NewEventService(st EventStore, repos *RepoService) *EventService {
	return &EventService{store: st, repos: repos}
}

// PublicFeed returns the global public timeline: events on public repositories
// that are themselves public, newest first.
func (s *EventService) PublicFeed(ctx context.Context, perPage int) ([]Event, error) {
	rows, err := s.store.ListEvents(ctx, store.EventFilter{PublicOnly: true, Limit: perPage})
	if err != nil {
		return nil, err
	}
	return s.toEvents(ctx, rows)
}

// RepoFeed returns a repository's timeline. The visibility check is the repo
// service's: a viewer who cannot see the repository gets ErrRepoNotFound, never
// a leak, and a viewer who can see a private repository sees its private events.
func (s *EventService) RepoFeed(ctx context.Context, viewerPK int64, owner, name string, perPage int) ([]Event, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListEvents(ctx, store.EventFilter{RepoPK: &repo.PK, Limit: perPage})
	if err != nil {
		return nil, err
	}
	return s.toEvents(ctx, rows)
}

// UserFeed returns the events a user performed. A viewer reading their own feed
// sees their private activity; any other viewer sees only the public subset.
func (s *EventService) UserFeed(ctx context.Context, viewerPK int64, login string, perPage int) ([]Event, error) {
	u, err := s.store.UserByLogin(ctx, login)
	if err != nil {
		return nil, ErrUserNotFound
	}
	rows, err := s.store.ListEvents(ctx, store.EventFilter{
		ActorPK:    &u.PK,
		PublicOnly: viewerPK != u.PK,
		Limit:      perPage,
	})
	if err != nil {
		return nil, err
	}
	return s.toEvents(ctx, rows)
}

// toEvents resolves each row's actor and repository into the compact objects the
// feed embeds and maps the stored fields onto the domain view. Actor and repo
// are cached across the page so a busy actor or repository is resolved once.
func (s *EventService) toEvents(ctx context.Context, rows []store.EventRow) ([]Event, error) {
	actors := map[int64]*User{}
	repos := map[int64]*Repo{}
	out := make([]Event, 0, len(rows))
	for i := range rows {
		r := rows[i]
		actor, ok := actors[r.ActorPK]
		if !ok {
			row, err := s.store.UserByPK(ctx, r.ActorPK)
			if err != nil {
				return nil, err
			}
			actor = userFromRow(row)
			actors[r.ActorPK] = actor
		}
		repo, ok := repos[r.RepoPK]
		if !ok {
			rp, err := s.repos.RepoForEvent(ctx, r.RepoPK)
			if err != nil {
				return nil, err
			}
			repo = rp
			repos[r.RepoPK] = repo
		}
		out = append(out, Event{
			ID:        r.DBID,
			Type:      eventType(r.Event),
			Actor:     actor,
			Repo:      repo,
			Payload:   json.RawMessage(r.Payload),
			Public:    r.Public,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}
