package domain

import (
	"context"
	"encoding/json"

	"github.com/tamnd/githome/store"
)

// Dispatch records a repository_dispatch event, the body of
// POST /repos/{owner}/{repo}/dispatches that CI uses to trigger a custom
// workflow. It needs write access; the caller-chosen event_type and the opaque
// client_payload ride along to the webhook fan-out, which delivers a
// repository_dispatch event to every subscribed hook. The dispatch never
// appears on the public activity timeline, matching GitHub.
func (s *RepoService) Dispatch(ctx context.Context, actorPK int64, owner, name, eventType string, clientPayload json.RawMessage) error {
	repo, err := s.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	recordEventFull(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventRepositoryDispatch,
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		Public:  false,
	}, nil, nil, &EventDetail{Action: eventType, ClientPayload: clientPayload})
	return nil
}
