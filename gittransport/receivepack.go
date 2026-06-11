package gittransport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
)

// Content types from the Git Smart HTTP protocol, push side.
const (
	ctReceiveAdvert  = "application/x-git-receive-pack-advertisement"
	ctReceiveResult  = "application/x-git-receive-pack-result"
	ctReceiveRequest = "application/x-git-receive-pack-request"
)

// advertiseReceive streams the receive-pack ref advertisement after authorizing
// write access. Unlike the upload side it requires authentication: an anonymous
// or read-only actor is refused before the advertisement so a push never gets
// as far as sending a pack.
func (s *Service) advertiseReceive(c *mizu.Ctx) error {
	w, r := c.Writer(), c.Request()
	repo, ok := s.resolveWrite(c)
	if !ok {
		return nil
	}
	bare := s.Git.Dir(repo.PK)

	w.Header().Set("Content-Type", ctReceiveAdvert)
	noCache(w.Header())
	w.WriteHeader(http.StatusOK)

	if err := writePktString(w, "# service=git-receive-pack\n"); err != nil {
		return nil
	}
	if err := writeFlush(w); err != nil {
		return nil
	}

	cmd := s.gitCommand(r.Context(), r, "receive-pack", "--stateless-rpc", "--advertise-refs", bare)
	cmd.Stdout = w
	if err := cmd.Run(); err != nil && s.Log != nil {
		s.Log.Error("receive-pack advertise failed", "err", err)
	}
	return nil
}

// handleReceivePack answers the push POST. It runs the real git receive-pack,
// which writes the incoming objects and updates the refs, then recovers the
// ref-update batch by diffing a ref snapshot taken before the run against one
// taken after and hands it to the domain push sink. This is the in-process
// post-receive design sanctioned by spec doc 04 section 6.3: the single
// database-sync entry point (pushed_at, push event, search reindex) runs inline
// rather than through the unix-socket hook callback, which is deferred along
// with SSH transport.
func (s *Service) handleReceivePack(c *mizu.Ctx) error {
	w, r := c.Writer(), c.Request()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, ctReceiveRequest) {
		http.Error(w, "bad content type", http.StatusUnsupportedMediaType)
		return nil
	}

	repo, ok := s.resolveWrite(c)
	if !ok {
		return nil
	}
	bare := s.Git.Dir(repo.PK)
	ctx := r.Context()

	before, err := s.Git.RefSnapshot(ctx, repo.PK)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("ref snapshot before push failed", "err", err)
		}
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return nil
	}

	body, err := requestBody(r, s.maxBody())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", ctReceiveResult)
	noCache(w.Header())
	w.WriteHeader(http.StatusOK)

	cmd := s.gitCommand(ctx, r, "receive-pack", "--stateless-rpc", bare)
	cmd.Stdin = body
	cmd.Stdout = &flushWriter{w: w}
	runErr := cmd.Run()
	if runErr != nil && s.Log != nil {
		s.Log.Error("receive-pack failed", "err", runErr)
	}

	// The push may have written a new packfile that warm go-git handles would
	// never see (their pack index is parsed once). Drop the cached handles before
	// the post-receive sync below reads the new objects.
	s.Git.InvalidateRepo(repo.PK)

	// Sync metadata from the refs git actually wrote. A receive-pack failure
	// (rejected push) leaves the refs unchanged, so the diff comes back empty and
	// OnPush is a no-op; on success the diff is the accepted batch.
	after, err := s.Git.RefSnapshot(ctx, repo.PK)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("ref snapshot after push failed", "err", err)
		}
		return nil
	}
	updates := diffSnapshots(before, after)
	if len(updates) == 0 {
		return nil
	}
	actor := auth.ActorFrom(ctx)
	batch := domain.PushBatch{
		RepoPK:     repo.PK,
		PusherPK:   actor.UserID,
		Protocol:   "http",
		Updates:    updates,
		ReceivedAt: time.Now(),
	}
	if err := s.Repos.OnPush(ctx, batch); err != nil && s.Log != nil {
		// The objects and refs are already durable; a sink failure is metadata
		// lag, not data loss, so the push still reports success to the client.
		s.Log.Error("post-receive sink failed", "err", err)
	}
	s.syncPulls(ctx, batch.PusherPK, repo.PK, updates)
	return nil
}

// syncPulls advances any open pull request that tracks a pushed branch as its
// head or base, then requeues its mergeability. It runs after the repo sync, off
// the same accepted-ref batch, and only on branch updates: a deleted branch or a
// tag push moves no pull request. A failure is logged, not surfaced, since the
// push is already durable. pusherPK travels through so the synchronize event a
// head move emits names the pusher as its actor.
func (s *Service) syncPulls(ctx context.Context, pusherPK, repoPK int64, updates []domain.RefUpdate) {
	if s.Pulls == nil {
		return
	}
	for _, u := range updates {
		branch, ok := strings.CutPrefix(u.Ref, "refs/heads/")
		if !ok || u.Deleted() {
			continue
		}
		if err := s.Pulls.OnHeadPush(ctx, pusherPK, repoPK, branch, u.NewSHA); err != nil && s.Log != nil {
			s.Log.Error("pull request head sync failed", "ref", u.Ref, "err", err)
		}
	}
}

// diffSnapshots turns a before/after ref map into the moved-ref batch: refs only
// in after were created, refs only in before were deleted, refs whose sha
// changed were updated. The all-zero sha marks the absent side, matching git's
// own post-receive line format.
func diffSnapshots(before, after map[string]git.SHA) []domain.RefUpdate {
	var updates []domain.RefUpdate
	for ref, newSHA := range after {
		oldSHA, existed := before[ref]
		if !existed {
			updates = append(updates, domain.RefUpdate{Ref: ref, OldSHA: domain.ZeroSHA, NewSHA: newSHA})
			continue
		}
		if oldSHA != newSHA {
			updates = append(updates, domain.RefUpdate{Ref: ref, OldSHA: oldSHA, NewSHA: newSHA})
		}
	}
	for ref, oldSHA := range before {
		if _, stillThere := after[ref]; !stillThere {
			updates = append(updates, domain.RefUpdate{Ref: ref, OldSHA: oldSHA, NewSHA: domain.ZeroSHA})
		}
	}
	return updates
}

// resolveWrite authorizes the request actor's write access and returns the
// repository. It requires authentication: an anonymous actor gets 401 with a
// Basic challenge (so git prompts for credentials), an authenticated actor
// without write access gets 403, and a repository the actor cannot see stays
// 404 so a private repo's existence never leaks. On any failure it writes the
// status and reports ok=false.
func (s *Service) resolveWrite(c *mizu.Ctx) (*domain.Repo, bool) {
	w := c.Writer()
	actor, err := s.actorFor(c)
	if err != nil || !actor.IsAuthenticated() {
		w.Header().Set("WWW-Authenticate", `Basic realm="githome"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	ctx := c.Request().Context()
	owner := c.Param("owner")
	name := strings.TrimSuffix(c.Param("repo"), ".git")

	repo, err := s.Repos.AuthorizeWrite(ctx, actor.UserID, owner, name)
	switch {
	case errors.Is(err, domain.ErrRepoNotFound):
		http.Error(w, "Not Found", http.StatusNotFound)
		return nil, false
	case errors.Is(err, domain.ErrForbidden):
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil, false
	case err != nil:
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return nil, false
	}
	return repo, true
}
