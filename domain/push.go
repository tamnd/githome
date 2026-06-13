package domain

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// ZeroSHA is the all-zero object id git uses in a post-receive line to mark a
// created ref (as the old value) or a deleted ref (as the new value).
const ZeroSHA = "0000000000000000000000000000000000000000"

// The job kinds the push sink enqueues. The workers that consume them land with
// the milestones that own each kind (webhook fan-out in M7, mergeability in M5,
// search in a later pass); defining the kinds here, where they are produced,
// lets the worker package depend on domain rather than the other way around.
const (
	JobReindexSearch           = "reindex_search"
	JobRecomputeMergeability   = "recompute_mergeability"
	JobRecomputeReviewDecision = "recompute_review_decision"
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

	repo, err := s.store.RepoByPK(ctx, b.RepoPK)
	if err != nil {
		return err
	}

	// Record the push event and fan it out to the repository's webhooks. The
	// moved refs have no home in a table, so they ride along on the fan-out job
	// for the renderer to build the push body from.
	recordEvent(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventPush,
		ActorPK: b.PusherPK,
		RepoPK:  b.RepoPK,
		Public:  !repo.Private,
	}, &PushPayload{
		RepoPK:   b.RepoPK,
		PusherPK: b.PusherPK,
		Protocol: b.Protocol,
		Updates:  b.Updates,
	})

	defaultRef := "refs/heads/" + repo.DefaultBranch
	for _, u := range b.Updates {
		if u.Ref == defaultRef && !u.Deleted() {
			key := "reindex:repo:" + strconv.FormatInt(b.RepoPK, 10)
			payload := `{"repo_pk":` + strconv.FormatInt(b.RepoPK, 10) + `}`
			if _, err := s.enq.Enqueue(ctx, JobReindexSearch, payload, key); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// SyntheticHeadPush builds the push a hook test fires: a single update of the
// repository's default branch, before and after both pinned to its current
// head, so the body renders against real refs without moving anything. ok is
// false when the default branch has no commit yet (an empty repository), where
// there is nothing to test against and GitHub sends no delivery. pusherPK names
// the actor who triggered the test.
func (s *RepoService) SyntheticHeadPush(ctx context.Context, repoPK int64, defaultBranch string, pusherPK int64) (*PushPayload, bool, error) {
	ref := "refs/heads/" + defaultBranch
	sha, err := s.gitStore.RefSHA(ctx, repoPK, ref)
	if errors.Is(err, git.ErrRefNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &PushPayload{
		RepoPK:   repoPK,
		PusherPK: pusherPK,
		Protocol: "http",
		Updates: []RefUpdate{{
			Ref:    ref,
			OldSHA: sha,
			NewSHA: sha,
		}},
	}, true, nil
}
