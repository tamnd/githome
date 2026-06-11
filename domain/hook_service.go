package domain

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// ErrHookNotFound is returned when no webhook or delivery matches the lookup in
// a repository the actor administers.
var ErrHookNotFound = errors.New("domain: hook not found")

// OrgHookRepo is the name of the synthetic repository an organization's hooks
// anchor on. Org hooks reuse the repo hook storage wholesale: they live on a
// hidden {org}/_org repository row created on first use, and the fan-out side
// looks the anchor up by this name to deliver an org's repo events to them.
const OrgHookRepo = "_org"

// HookStore is the slice of the store the hook service writes and reads through:
// the webhook CRUD, its delivery history, the queue the redeliver replays
// through, and the lookups the org-hook anchor repo is resolved and created by.
type HookStore interface {
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	InsertRepo(ctx context.Context, r *store.RepoRow) error

	InsertWebhook(ctx context.Context, w *store.WebhookRow) error
	GetWebhookForRepo(ctx context.Context, repoPK, dbID int64) (*store.WebhookRow, error)
	ListWebhooks(ctx context.Context, repoPK int64) ([]store.WebhookRow, error)
	UpdateWebhook(ctx context.Context, w *store.WebhookRow) error
	DeleteWebhook(ctx context.Context, repoPK, dbID int64) error
	ListDeliveries(ctx context.Context, webhookPK int64, limit int) ([]store.WebhookDeliveryRow, error)
	GetDeliveryForWebhook(ctx context.Context, webhookPK, dbID int64) (*store.WebhookDeliveryRow, error)
}

// HookService manages a repository's webhooks. Webhook administration needs the
// same authority as writing to the repository, so every method authorizes write
// access first; the secret a hook carries is held but never returned, so the
// service maps each row to a domain view that omits it.
type HookService struct {
	store HookStore
	repos *RepoService
	enq   worker.Enqueuer
}

// NewHookService builds a HookService over the store and the repo service.
func NewHookService(st HookStore, repos *RepoService, enq worker.Enqueuer) *HookService {
	return &HookService{store: st, repos: repos, enq: enq}
}

// HookInput is the create payload: the delivery URL, the content type, an
// optional signing secret, the TLS-verification setting, the active flag, and
// the subscribed event names. An empty event list defaults to push, matching
// GitHub.
type HookInput struct {
	Name        string
	URL         string
	ContentType string
	Secret      *string
	InsecureSSL bool
	Active      *bool
	Events      []string
}

// HookPatch is the edit payload. A nil field is left unchanged. AddEvents and
// RemoveEvents adjust the subscription incrementally the way the REST config
// endpoint does, applied after a wholesale Events replacement.
type HookPatch struct {
	URL          *string
	ContentType  *string
	Secret       *string
	InsecureSSL  *bool
	Active       *bool
	Events       *[]string
	AddEvents    []string
	RemoveEvents []string
}

// CreateHook registers a webhook on the repository after authorizing write
// access and validating the URL and content type.
func (s *HookService) CreateHook(ctx context.Context, actorPK int64, owner, name string, in HookInput) (*Hook, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if err := validateHookURL(in.URL); err != nil {
		return nil, err
	}
	contentType, err := normalizeContentType(in.ContentType)
	if err != nil {
		return nil, err
	}
	events := normalizeEvents(in.Events)
	row := &store.WebhookRow{
		RepoPK:      repo.PK,
		Name:        firstNonEmpty(in.Name, "web"),
		URL:         in.URL,
		ContentType: contentType,
		Secret:      emptyToNil(in.Secret),
		InsecureSSL: in.InsecureSSL,
		Active:      in.Active == nil || *in.Active,
		Events:      marshalEvents(events),
	}
	if err := s.store.InsertWebhook(ctx, row); err != nil {
		return nil, err
	}
	// A new hook gets a ping right away, the way GitHub confirms a fresh
	// endpoint can receive deliveries.
	s.enqueuePing(ctx, row.PK, actorPK)
	return hookFromRow(row), nil
}

// CreateOrgHook registers an organization-level webhook. The hook is stored as
// a repo hook on the org's synthetic anchor repository, created here on first
// use, so every read and edit after this goes through the ordinary hook methods
// addressed at (org, OrgHookRepo).
func (s *HookService) CreateOrgHook(ctx context.Context, actorPK int64, org string, in HookInput) (*Hook, error) {
	if err := s.ensureOrgAnchor(ctx, actorPK, org); err != nil {
		return nil, err
	}
	return s.CreateHook(ctx, actorPK, org, OrgHookRepo, in)
}

// ListOrgHooks returns an organization's webhooks. An org that never created a
// hook has no anchor repository; that is an empty list, not an error.
func (s *HookService) ListOrgHooks(ctx context.Context, actorPK int64, org string) ([]Hook, error) {
	hooks, err := s.ListHooks(ctx, actorPK, org, OrgHookRepo)
	if errors.Is(err, ErrRepoNotFound) {
		return []Hook{}, nil
	}
	return hooks, err
}

// ensureOrgAnchor resolves the org's anchor repository, creating it when the
// org has never had a hook. The actor rule mirrors repo creation under an org:
// the account itself or a site admin. The anchor is a private metadata row
// only; no git repository backs it.
func (s *HookService) ensureOrgAnchor(ctx context.Context, actorPK int64, org string) error {
	orgRow, err := s.store.UserByLogin(ctx, org)
	if errors.Is(err, store.ErrNotFound) {
		return ErrRepoNotFound
	}
	if err != nil {
		return err
	}
	if orgRow.PK != actorPK && !orgRow.SiteAdmin {
		return ErrForbidden
	}
	if _, err := s.store.RepoByOwnerName(ctx, org, OrgHookRepo); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return s.store.InsertRepo(ctx, &store.RepoRow{
		OwnerPK:       orgRow.PK,
		Name:          OrgHookRepo,
		Private:       true,
		DefaultBranch: "main",
	})
}

// PingHook submits a ping delivery to the webhook: the {zen, hook_id, hook}
// body GitHub sends so an endpoint can be exercised without waiting for a real
// event. The same authorization as every other hook read applies.
func (s *HookService) PingHook(ctx context.Context, actorPK int64, owner, name string, hookID int64) error {
	_, row, err := s.loadHook(ctx, actorPK, owner, name, hookID)
	if err != nil {
		return err
	}
	s.enqueuePing(ctx, row.PK, actorPK)
	return nil
}

// enqueuePing submits one ping delivery job. Like event fan-out it is
// best-effort: a queue failure never fails the caller's write.
func (s *HookService) enqueuePing(ctx context.Context, webhookPK, actorPK int64) {
	body, err := json.Marshal(DeliverWebhookPayload{WebhookPK: webhookPK, Ping: true, SenderPK: actorPK})
	if err != nil {
		return
	}
	_, _ = s.enq.Enqueue(ctx, JobDeliverWebhook, string(body), "")
}

// ListHooks returns the repository's webhooks, secrets omitted.
func (s *HookService) ListHooks(ctx context.Context, actorPK int64, owner, name string) ([]Hook, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListWebhooks(ctx, repo.PK)
	if err != nil {
		return nil, err
	}
	out := make([]Hook, 0, len(rows))
	for i := range rows {
		out = append(out, *hookFromRow(&rows[i]))
	}
	return out, nil
}

// GetHook resolves one webhook by its public id, secret omitted.
func (s *HookService) GetHook(ctx context.Context, actorPK int64, owner, name string, hookID int64) (*Hook, error) {
	_, row, err := s.loadHook(ctx, actorPK, owner, name, hookID)
	if err != nil {
		return nil, err
	}
	return hookFromRow(row), nil
}

// UpdateHook applies a patch to a webhook and returns its domain view.
func (s *HookService) UpdateHook(ctx context.Context, actorPK int64, owner, name string, hookID int64, p HookPatch) (*Hook, error) {
	_, row, err := s.loadHook(ctx, actorPK, owner, name, hookID)
	if err != nil {
		return nil, err
	}
	if p.URL != nil {
		if err := validateHookURL(*p.URL); err != nil {
			return nil, err
		}
		row.URL = *p.URL
	}
	if p.ContentType != nil {
		ct, err := normalizeContentType(*p.ContentType)
		if err != nil {
			return nil, err
		}
		row.ContentType = ct
	}
	if p.Secret != nil {
		row.Secret = emptyToNil(p.Secret)
	}
	if p.InsecureSSL != nil {
		row.InsecureSSL = *p.InsecureSSL
	}
	if p.Active != nil {
		row.Active = *p.Active
	}
	events := unmarshalEvents(row.Events)
	if p.Events != nil {
		events = normalizeEvents(*p.Events)
	}
	events = addEvents(events, p.AddEvents)
	events = removeEvents(events, p.RemoveEvents)
	row.Events = marshalEvents(events)
	if err := s.store.UpdateWebhook(ctx, row); err != nil {
		return nil, err
	}
	return hookFromRow(row), nil
}

// DeleteHook removes a webhook and its deliveries.
func (s *HookService) DeleteHook(ctx context.Context, actorPK int64, owner, name string, hookID int64) error {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	if err := s.store.DeleteWebhook(ctx, repo.PK, hookID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrHookNotFound
		}
		return err
	}
	return nil
}

// ListHookDeliveries returns a webhook's recent deliveries, newest first.
func (s *HookService) ListHookDeliveries(ctx context.Context, actorPK int64, owner, name string, hookID int64, perPage int) ([]HookDelivery, error) {
	_, row, err := s.loadHook(ctx, actorPK, owner, name, hookID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListDeliveries(ctx, row.PK, perPage)
	if err != nil {
		return nil, err
	}
	out := make([]HookDelivery, 0, len(rows))
	for i := range rows {
		out = append(out, *deliveryFromRow(&rows[i], false))
	}
	return out, nil
}

// GetHookDelivery resolves one delivery of a webhook by its public id, with the
// full request and response record.
func (s *HookService) GetHookDelivery(ctx context.Context, actorPK int64, owner, name string, hookID, deliveryID int64) (*HookDelivery, error) {
	_, row, err := s.loadHook(ctx, actorPK, owner, name, hookID)
	if err != nil {
		return nil, err
	}
	d, err := s.store.GetDeliveryForWebhook(ctx, row.PK, deliveryID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrHookNotFound
	}
	if err != nil {
		return nil, err
	}
	return deliveryFromRow(d, true), nil
}

// RedeliverHookDelivery enqueues a replay of a recorded delivery: the worker
// re-sends the stored request and records a new delivery marked as a redelivery.
func (s *HookService) RedeliverHookDelivery(ctx context.Context, actorPK int64, owner, name string, hookID, deliveryID int64) error {
	_, row, err := s.loadHook(ctx, actorPK, owner, name, hookID)
	if err != nil {
		return err
	}
	d, err := s.store.GetDeliveryForWebhook(ctx, row.PK, deliveryID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrHookNotFound
	}
	if err != nil {
		return err
	}
	payload, err := json.Marshal(DeliverWebhookPayload{WebhookPK: row.PK, RedeliverOf: d.PK})
	if err != nil {
		return err
	}
	_, err = s.enq.Enqueue(ctx, JobDeliverWebhook, string(payload), "")
	return err
}

// loadHook authorizes write access and resolves the webhook by its public id
// scoped to the repository.
func (s *HookService) loadHook(ctx context.Context, actorPK int64, owner, name string, hookID int64) (*Repo, *store.WebhookRow, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, nil, err
	}
	row, err := s.store.GetWebhookForRepo(ctx, repo.PK, hookID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrHookNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return repo, row, nil
}

// validateHookURL requires an absolute http or https URL; the SSRF guard on the
// delivery side enforces the host policy.
func validateHookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ErrValidation
	}
	return nil
}

// normalizeContentType accepts json or form, defaulting an empty value to json.
func normalizeContentType(ct string) (string, error) {
	switch strings.TrimSpace(ct) {
	case "", "json":
		return "json", nil
	case "form":
		return "form", nil
	default:
		return "", ErrValidation
	}
}

// normalizeEvents trims and lowercases the subscription, defaulting an empty
// list to push and collapsing a wildcard to the single "*" entry.
func normalizeEvents(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		if e == "*" {
			return []string{"*"}
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return []string{EventPush}
	}
	return out
}

func addEvents(base, add []string) []string {
	if len(add) == 0 {
		return base
	}
	return normalizeEvents(append(append([]string{}, base...), add...))
}

func removeEvents(base, remove []string) []string {
	if len(remove) == 0 {
		return base
	}
	drop := map[string]bool{}
	for _, e := range remove {
		drop[strings.ToLower(strings.TrimSpace(e))] = true
	}
	var out []string
	for _, e := range base {
		if !drop[e] {
			out = append(out, e)
		}
	}
	return out
}

func marshalEvents(events []string) string {
	if len(events) == 0 {
		events = []string{EventPush}
	}
	b, err := json.Marshal(events)
	if err != nil {
		return `["push"]`
	}
	return string(b)
}

func unmarshalEvents(raw string) []string {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) == "" {
		return b
	}
	return a
}

func emptyToNil(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}
