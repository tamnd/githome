package webhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// maxResponseBytes bounds how much of a receiver's response body a delivery
// records, so a hostile or chatty endpoint cannot make the store grow without
// limit. The headers and status are always recorded in full.
const maxResponseBytes = 64 << 10

// Store is the slice of the metadata store the delivery worker drives: the event
// it renders, the hooks it fans out to, and the delivery history it records. The
// concrete store satisfies it directly.
type Store interface {
	GetEventByPK(ctx context.Context, pk int64) (*store.EventRow, error)
	SetEventPayload(ctx context.Context, pk int64, payload string) error
	ListActiveWebhooks(ctx context.Context, repoPK int64) ([]store.WebhookRow, error)
	RepoByPK(ctx context.Context, pk int64) (*store.RepoRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	GetWebhookByPK(ctx context.Context, pk int64) (*store.WebhookRow, error)
	SetWebhookLastResponse(ctx context.Context, pk int64, summary string) error
	InsertDelivery(ctx context.Context, d *store.WebhookDeliveryRow) error
	GetDeliveryByPK(ctx context.Context, pk int64) (*store.WebhookDeliveryRow, error)
}

// Deliverer holds the wiring the two webhook job handlers share: the store, the
// renderer, the guarded HTTP client, the enqueuer the fan-out submits delivery
// jobs through, and the version string the User-Agent carries.
type Deliverer struct {
	store    Store
	renderer *Renderer
	client   *http.Client
	enq      worker.Enqueuer
	version  string
}

// NewDeliverer wires a Deliverer. A nil client uses a default guarded client
// that blocks private and loopback destinations.
func NewDeliverer(st Store, r *Renderer, client *http.Client, enq worker.Enqueuer, version string) *Deliverer {
	if client == nil {
		client = NewClient(ClientOptions{})
	}
	if version == "" {
		version = "githome"
	}
	return &Deliverer{store: st, renderer: r, client: client, enq: enq, version: version}
}

// DeliverEventHandler binds the deliver_event kind: it loads the recorded event,
// renders its payload, stores the rendered feed payload back on the event, and
// enqueues one deliver_webhook job per active hook subscribed to the event. A
// missing or unrenderable event is a permanent error; a transient store or
// enqueue failure returns an error so the queue retries the whole fan-out.
func (d *Deliverer) DeliverEventHandler() worker.Handler {
	return func(ctx context.Context, job store.JobRow) error {
		var p domain.DeliverEventPayload
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return fmt.Errorf("deliver_event: bad payload: %w", err)
		}
		if p.EventPK == 0 {
			return fmt.Errorf("deliver_event: missing event_pk")
		}
		ev, err := d.store.GetEventByPK(ctx, p.EventPK)
		if err != nil {
			return err
		}
		rendered, err := d.renderer.Render(ctx, ev, p.Push, p.CreateDelete, p.Detail)
		if err != nil {
			return err
		}
		if err := d.store.SetEventPayload(ctx, ev.PK, string(rendered.Payload)); err != nil {
			return err
		}
		hooks, err := d.store.ListActiveWebhooks(ctx, ev.RepoPK)
		if err != nil {
			return err
		}
		orgHooks, err := d.orgHooks(ctx, ev.RepoPK)
		if err != nil {
			return err
		}
		hooks = append(hooks, orgHooks...)
		for i := range hooks {
			if !subscribed(&hooks[i], ev.Event) {
				continue
			}
			body, err := json.Marshal(domain.DeliverWebhookPayload{WebhookPK: hooks[i].PK, EventPK: ev.PK, Push: p.Push, CreateDelete: p.CreateDelete, Detail: p.Detail})
			if err != nil {
				return err
			}
			if _, err := d.enq.Enqueue(ctx, domain.JobDeliverWebhook, string(body), ""); err != nil {
				return err
			}
		}
		return nil
	}
}

// orgHooks returns the active webhooks on the repo owner's org-hook anchor
// repository, the org-scoped half of the fan-out: an org hook receives every
// event from every repository the org owns. An owner without an anchor has no
// org hooks; a store failure propagates so the queue retries the fan-out.
func (d *Deliverer) orgHooks(ctx context.Context, repoPK int64) ([]store.WebhookRow, error) {
	repo, err := d.store.RepoByPK(ctx, repoPK)
	if err != nil {
		return nil, err
	}
	if repo.Name == domain.OrgHookRepo {
		// The event is on the anchor itself; its hooks are already listed.
		return nil, nil
	}
	owner, err := d.store.UserByPK(ctx, repo.OwnerPK)
	if err != nil {
		return nil, err
	}
	anchor, err := d.store.RepoByOwnerName(ctx, owner.Login, domain.OrgHookRepo)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d.store.ListActiveWebhooks(ctx, anchor.PK)
}

// DeliverWebhookHandler binds the deliver_webhook kind: it loads the hook, builds
// the request (rendering the event afresh, or replaying a recorded delivery when
// the job is a redelivery), POSTs it behind the SSRF guard, records the result,
// and updates the hook's last response. A non-2xx response returns an error so
// the queue retries, accepting at-least-once delivery.
func (d *Deliverer) DeliverWebhookHandler() worker.Handler {
	return func(ctx context.Context, job store.JobRow) error {
		var p domain.DeliverWebhookPayload
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return fmt.Errorf("deliver_webhook: bad payload: %w", err)
		}
		if p.WebhookPK == 0 {
			return fmt.Errorf("deliver_webhook: missing webhook_pk")
		}
		hook, err := d.store.GetWebhookByPK(ctx, p.WebhookPK)
		if err != nil {
			return err
		}
		req, err := d.buildRequest(ctx, hook, &p)
		if err != nil {
			return err
		}
		return d.send(ctx, hook, req)
	}
}

// outRequest is the prepared HTTP request a delivery sends and records: the
// target, the headers, the exact body bytes, and the event coordinates the
// delivery row stores.
type outRequest struct {
	url        string
	headers    map[string]string
	body       []byte
	event      string
	action     string
	guid       string
	redelivery bool
}

// buildRequest prepares the outgoing request, either by rendering the event or,
// for a redelivery, by replaying a recorded delivery's stored request under a
// fresh delivery guid.
func (d *Deliverer) buildRequest(ctx context.Context, hook *store.WebhookRow, p *domain.DeliverWebhookPayload) (*outRequest, error) {
	guid, err := newGUID()
	if err != nil {
		return nil, err
	}
	if p.RedeliverOf != 0 {
		prior, err := d.store.GetDeliveryByPK(ctx, p.RedeliverOf)
		if err != nil {
			return nil, err
		}
		headers := map[string]string{}
		_ = json.Unmarshal([]byte(prior.RequestHeaders), &headers)
		headers["X-GitHub-Delivery"] = guid
		return &outRequest{
			url:        prior.RequestURL,
			headers:    headers,
			body:       []byte(prior.RequestBody),
			event:      prior.Event,
			action:     prior.Action,
			guid:       guid,
			redelivery: true,
		}, nil
	}
	var rendered *Rendered
	if p.Ping {
		// A ping has no event row: the body renders straight from the hook.
		if rendered, err = d.renderer.RenderPing(ctx, hook, p.SenderPK); err != nil {
			return nil, err
		}
	} else {
		ev, err := d.store.GetEventByPK(ctx, p.EventPK)
		if err != nil {
			return nil, err
		}
		if rendered, err = d.renderer.Render(ctx, ev, p.Push, p.CreateDelete, p.Detail); err != nil {
			return nil, err
		}
	}
	body, contentType := encodeBody(hook.ContentType, rendered.Body)
	headers := map[string]string{
		"Content-Type":                           contentType,
		"Accept":                                 "*/*",
		"User-Agent":                             "GitHub-Hookshot/" + d.version,
		"X-GitHub-Event":                         rendered.Event,
		"X-GitHub-Delivery":                      guid,
		"X-GitHub-Hook-ID":                       strconv.FormatInt(hook.DBID, 10),
		"X-GitHub-Hook-Installation-Target-Type": "repository",
		"X-GitHub-Hook-Installation-Target-ID":   strconv.FormatInt(rendered.RepositoryID, 10),
	}
	if hook.Secret != nil && *hook.Secret != "" {
		sig := Sign(*hook.Secret, body)
		headers["X-Hub-Signature-256"] = sig.SHA256
		headers["X-Hub-Signature"] = sig.SHA1
	}
	return &outRequest{
		url:     hook.URL,
		headers: headers,
		body:    body,
		event:   rendered.Event,
		action:  rendered.Action,
		guid:    guid,
	}, nil
}

// send performs the POST, records the delivery, and updates the hook's last
// response. It returns an error on a transport failure or a non-2xx status so
// the queue retries.
func (d *Deliverer) send(ctx context.Context, hook *store.WebhookRow, req *outRequest) error {
	client := d.client
	if hook.InsecureSSL {
		client = insecureClient(d.client)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.url, bytes.NewReader(req.body))
	if err != nil {
		return err
	}
	for k, v := range req.headers {
		httpReq.Header.Set(k, v)
	}
	start := time.Now()
	resp, sendErr := client.Do(httpReq)
	duration := time.Since(start)

	row := &store.WebhookDeliveryRow{
		WebhookPK:      hook.PK,
		GUID:           req.guid,
		Event:          req.event,
		Action:         req.action,
		RequestURL:     req.url,
		RequestHeaders: encodeHeaders(req.headers),
		RequestBody:    string(req.body),
		DurationMS:     duration.Milliseconds(),
		Redelivery:     req.redelivery,
	}
	if sendErr != nil {
		row.ResponseBody = sendErr.Error()
		_ = d.store.InsertDelivery(ctx, row)
		d.recordLast(ctx, hook.PK, nil, "failed to connect", sendErr.Error())
		return fmt.Errorf("deliver_webhook: %w", sendErr)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	code := int64(resp.StatusCode)
	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	row.StatusCode = &code
	row.ResponseHeaders = encodeHeaders(flattenHeader(resp.Header))
	row.ResponseBody = string(respBody)
	row.Success = success
	_ = d.store.InsertDelivery(ctx, row)
	d.recordLast(ctx, hook.PK, &code, statusText(success, resp.StatusCode), "")
	if !success {
		return fmt.Errorf("deliver_webhook: hook %d returned %d", hook.DBID, resp.StatusCode)
	}
	return nil
}

// recordLast stores the compact last-response summary on the hook.
func (d *Deliverer) recordLast(ctx context.Context, hookPK int64, code *int64, status, message string) {
	summary := struct {
		Code    *int64 `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}{Code: code, Status: status, Message: message}
	b, err := json.Marshal(summary)
	if err != nil {
		return
	}
	_ = d.store.SetWebhookLastResponse(ctx, hookPK, string(b))
}

// subscribed reports whether a hook's event list covers the given event, where
// "*" subscribes to every event.
func subscribed(hook *store.WebhookRow, event string) bool {
	var events []string
	if err := json.Unmarshal([]byte(hook.Events), &events); err != nil {
		return false
	}
	for _, e := range events {
		if e == "*" || e == event {
			return true
		}
	}
	return false
}

// encodeBody renders the request body for the hook's content type. A form hook
// sends payload=<urlencoded json>; a json hook sends the raw JSON. The signature
// is computed over whichever bytes this returns.
func encodeBody(contentType string, jsonBody []byte) ([]byte, string) {
	if contentType == "form" {
		form := "payload=" + url.QueryEscape(string(jsonBody))
		return []byte(form), "application/x-www-form-urlencoded"
	}
	return jsonBody, "application/json"
}

// encodeHeaders serializes a header map to the JSON the delivery row stores.
func encodeHeaders(h map[string]string) string {
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// flattenHeader collapses an http.Header to a single value per key for recording.
func flattenHeader(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k := range h {
		out[k] = h.Get(k)
	}
	return out
}

// statusText renders the human status the last-response summary reports.
func statusText(success bool, code int) string {
	if success {
		return "OK"
	}
	return "Invalid HTTP Response: " + strconv.Itoa(code)
}

// newGUID returns a random RFC 4122-shaped delivery identifier.
func newGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
