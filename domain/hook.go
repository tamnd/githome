package domain

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/tamnd/githome/store"
)

// Hook is the domain view of a repository webhook the API returns. It never
// carries the signing secret: HasSecret reports only whether one is set, so the
// secret cannot leak through a presenter. The delivery worker reads the secret
// straight from the store, off this path.
type Hook struct {
	ID           int64
	Name         string
	Active       bool
	Events       []string
	Config       HookConfig
	LastResponse *HookResponse
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// HookConfig is the transport configuration of a hook minus the secret.
type HookConfig struct {
	URL         string
	ContentType string
	InsecureSSL bool
	HasSecret   bool
}

// HookResponse summarizes a hook's most recent delivery, the value the API
// reports as last_response.
type HookResponse struct {
	Code    *int
	Status  string
	Message *string
}

// HookDelivery is the domain view of one recorded delivery attempt. Request and
// Response are populated only for the single-delivery detail view; the list view
// leaves them nil.
type HookDelivery struct {
	ID           int64
	GUID         string
	Event        string
	Action       *string
	StatusCode   int
	Status       string
	Duration     float64
	Redelivery   bool
	RepositoryID *int64
	DeliveredAt  time.Time
	Request      *HookDeliveryHTTP
	Response     *HookDeliveryHTTP
}

// HookDeliveryHTTP is one half (request or response) of a delivery's full
// record: the headers and the body.
type HookDeliveryHTTP struct {
	Headers map[string]string
	Body    string
}

// HookForDelivery maps a stored webhook to its domain view for the delivery
// worker, which renders the hook object a ping body embeds. It is the same
// secret-free view every hook read returns.
func HookForDelivery(w *store.WebhookRow) *Hook {
	return hookFromRow(w)
}

// hookFromRow maps a stored webhook to its domain view, dropping the secret and
// decoding the parsed last-response summary.
func hookFromRow(w *store.WebhookRow) *Hook {
	h := &Hook{
		ID:     w.DBID,
		Name:   w.Name,
		Active: w.Active,
		Events: unmarshalEvents(w.Events),
		Config: HookConfig{
			URL:         w.URL,
			ContentType: w.ContentType,
			InsecureSSL: w.InsecureSSL,
			HasSecret:   w.Secret != nil && *w.Secret != "",
		},
		LastResponse: lastResponse(w.LastResponse),
		CreatedAt:    w.CreatedAt,
		UpdatedAt:    w.UpdatedAt,
	}
	return h
}

// lastResponse decodes the JSON summary stored on a hook into the domain shape,
// defaulting to the unused state GitHub reports before the first delivery.
func lastResponse(raw *string) *HookResponse {
	if raw == nil || *raw == "" {
		return &HookResponse{Status: "unused"}
	}
	var r HookResponse
	if err := json.Unmarshal([]byte(*raw), &r); err != nil {
		return &HookResponse{Status: "unused"}
	}
	return &r
}

// deliveryFromRow maps a stored delivery to its list-view domain shape. full
// adds the request and response halves the detail view carries.
func deliveryFromRow(d *store.WebhookDeliveryRow, full bool) *HookDelivery {
	out := &HookDelivery{
		ID:          d.DBID,
		GUID:        d.GUID,
		Event:       d.Event,
		Action:      emptyToNil(&d.Action),
		Status:      deliveryStatus(d.StatusCode, d.Success),
		Duration:    float64(d.DurationMS) / 1000,
		Redelivery:  d.Redelivery,
		DeliveredAt: d.CreatedAt,
	}
	if d.StatusCode != nil {
		out.StatusCode = int(*d.StatusCode)
	}
	if full {
		out.Request = &HookDeliveryHTTP{Headers: decodeHeaders(d.RequestHeaders), Body: d.RequestBody}
		out.Response = &HookDeliveryHTTP{Headers: decodeHeaders(d.ResponseHeaders), Body: d.ResponseBody}
	}
	return out
}

// deliveryStatus renders the human status a delivery reports: OK for a 2xx, the
// reason text otherwise, and "failed to connect" when no response arrived.
func deliveryStatus(code *int64, success bool) string {
	if code == nil {
		return "failed to connect"
	}
	if success {
		return "OK"
	}
	return "Invalid HTTP Response: " + strconv.FormatInt(*code, 10)
}

// decodeHeaders parses the stored JSON header map, returning an empty map on a
// malformed value rather than failing the read.
func decodeHeaders(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
