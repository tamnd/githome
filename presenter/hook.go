package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// secretMask is the fixed string GitHub returns for config.secret when a hook
// has a secret set; the real value is never emitted.
const secretMask = "********"

// Hook renders a repository webhook. The secret is never on the wire: when the
// hook has one, config.secret is the fixed mask, and when it does not the field
// is omitted, matching github.com.
func (b *URLBuilder) Hook(owner, repo string, h *domain.Hook) restmodel.Hook {
	base := b.RepoAPI(owner, repo) + "/hooks/" + strconv.FormatInt(h.ID, 10)
	cfg := restmodel.HookConfig{
		ContentType: h.Config.ContentType,
		InsecureSSL: insecureSSL(h.Config.InsecureSSL),
		URL:         h.Config.URL,
	}
	if h.Config.HasSecret {
		mask := secretMask
		cfg.Secret = &mask
	}
	return restmodel.Hook{
		Type:          "Repository",
		ID:            h.ID,
		Name:          h.Name,
		Active:        h.Active,
		Events:        h.Events,
		Config:        cfg,
		UpdatedAt:     restmodel.NewTime(h.UpdatedAt),
		CreatedAt:     restmodel.NewTime(h.CreatedAt),
		URL:           base,
		TestURL:       base + "/test",
		PingURL:       base + "/pings",
		DeliveriesURL: base + "/deliveries",
		LastResponse:  hookResponse(h.LastResponse),
	}
}

// HookDelivery renders one delivery. full controls whether the request and
// response bodies are present: the list omits them, the single-delivery GET
// includes them.
func (b *URLBuilder) HookDelivery(owner, repo string, hookID int64, d *domain.HookDelivery) restmodel.HookDelivery {
	out := restmodel.HookDelivery{
		ID:           d.ID,
		GUID:         d.GUID,
		DeliveredAt:  restmodel.NewTime(d.DeliveredAt),
		Redelivery:   d.Redelivery,
		Duration:     d.Duration,
		Status:       d.Status,
		StatusCode:   d.StatusCode,
		Event:        d.Event,
		Action:       d.Action,
		RepositoryID: d.RepositoryID,
	}
	if d.Request != nil || d.Response != nil {
		base := b.RepoAPI(owner, repo) + "/hooks/" + strconv.FormatInt(hookID, 10)
		out.URL = base + "/deliveries/" + strconv.FormatInt(d.ID, 10)
	}
	if d.Request != nil {
		out.Request = &restmodel.HookDeliveryRequest{
			Headers: d.Request.Headers,
			Payload: rawJSON(d.Request.Body),
		}
	}
	if d.Response != nil {
		out.Response = &restmodel.HookDeliveryResponse{
			Headers: d.Response.Headers,
			Payload: d.Response.Body,
		}
	}
	return out
}

// hookResponse renders the last-response summary, defaulting a nil value to the
// unused state.
func hookResponse(r *domain.HookResponse) restmodel.HookResponse {
	if r == nil {
		return restmodel.HookResponse{Status: "unused"}
	}
	return restmodel.HookResponse{Code: r.Code, Status: r.Status, Message: r.Message}
}

// insecureSSL maps the bool flag to the "0"/"1" string GitHub serializes.
func insecureSSL(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// rawJSON returns body as a JSON message, falling back to a JSON null when it is
// not valid JSON so the field is always well formed.
func rawJSON(body string) []byte {
	if body == "" {
		return []byte("null")
	}
	return []byte(body)
}
