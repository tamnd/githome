package domain

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

// ZeroSHA is the all-zero object id git uses in a post-receive line to mark a
// created ref (as the old value) or a deleted ref (as the new value).
const ZeroSHA = "0000000000000000000000000000000000000000"

// The job kinds the push sink enqueues. The workers that consume them land with
// the milestones that own each kind (push events and webhook delivery in M7,
// mergeability in M5, search in a later pass); defining the kinds here, where
// they are produced, lets the worker package depend on domain rather than the
// other way around.
const (
	JobPushEvent             = "push_event"
	JobReindexSearch         = "reindex_search"
	JobRecomputeMergeability = "recompute_mergeability"
)

// RefUpdate is one moved reference from a push.
type RefUpdate struct {
	Ref    string // fully qualified, e.g. "refs/heads/main"
	OldSHA string // ZeroSHA on create
	NewSHA string // ZeroSHA on delete
}

// Created reports whether the update brought a new ref into existence.
func (u RefUpdate) Created() bool { return u.OldSHA == ZeroSHA || u.OldSHA == "" }

// Deleted reports whether the update removed a ref.
func (u RefUpdate) Deleted() bool { return u.NewSHA == ZeroSHA || u.NewSHA == "" }

// PushBatch is the parsed post-receive batch the transport hands to OnPush: the
// repository, the authenticated pusher, the transport, and the moved refs.
type PushBatch struct {
	RepoPK     int64
	PusherPK   int64
	Protocol   string // "http" | "ssh"
	Updates    []RefUpdate
	ReceivedAt time.Time
}

// OnPush is the single database-sync entry point after a push. It advances the
// repository's pushed_at, records a push event for webhook delivery, and
// enqueues a search reindex when the default branch moved. The mergeability
// recompute per affected pull request lands with the pull-request milestone that
// introduces the pull_requests table; the dedupe key it will use is reserved by
// the enqueue helper below. The git objects and refs are already written by the
// time OnPush runs, so a failure here is a metadata-lag bug, not data loss.
func (s *RepoService) OnPush(ctx context.Context, b PushBatch) error {
	if len(b.Updates) == 0 {
		return nil
	}
	at := b.ReceivedAt
	if at.IsZero() {
		at = time.Now()
	}
	if err := s.store.TouchRepoPushedAt(ctx, b.RepoPK, at); err != nil {
		return err
	}

	payload, err := json.Marshal(pushEventPayload{
		RepoPK:   b.RepoPK,
		PusherPK: b.PusherPK,
		Protocol: b.Protocol,
		Updates:  b.Updates,
	})
	if err != nil {
		return err
	}
	if _, err := s.enq.Enqueue(ctx, JobPushEvent, string(payload), ""); err != nil {
		return err
	}

	repo, err := s.store.RepoByPK(ctx, b.RepoPK)
	if err != nil {
		return err
	}
	defaultRef := "refs/heads/" + repo.DefaultBranch
	for _, u := range b.Updates {
		if u.Ref == defaultRef && !u.Deleted() {
			key := "reindex:repo:" + strconv.FormatInt(b.RepoPK, 10)
			if _, err := s.enq.Enqueue(ctx, JobReindexSearch, "", key); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// pushEventPayload is the JSON body of an enqueued push_event job. The webhook
// worker decodes it in M7 to build the push webhook delivery.
type pushEventPayload struct {
	RepoPK   int64       `json:"repo_pk"`
	PusherPK int64       `json:"pusher_pk"`
	Protocol string      `json:"protocol"`
	Updates  []RefUpdate `json:"updates"`
}
