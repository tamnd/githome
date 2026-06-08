package reposettings

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// hooks.go holds the repository webhooks section: the list, the shared
// create-and-edit form, the delete, and a recorded delivery's detail and replay.
// Every handler reads and writes through the domain HookService the REST surface
// uses, so a hook the page creates is the same hook the API returns, and the
// secret a hook carries is never rendered back: the form shows only whether one is
// set and a save that leaves the field blank keeps the stored secret.

// deliveryHistoryLimit is how many recent deliveries the edit page lists, enough
// to show whether the endpoint is answering without paging.
const deliveryHistoryLimit = 20

// Root redirects the bare repository settings root to the first backed section so
// a bookmark of /{owner}/{repo}/settings keeps landing on a real page.
func (h *Handlers) Root(c *mizu.Ctx) error {
	return h.redirect(c, route.RepoHooks(h.owner(c), h.name(c)))
}

// Hooks renders the repository's webhooks list, a row per hook with its delivery
// target, active state, subscription summary, and last-delivery status.
func (h *Handlers) Hooks(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	list, err := h.hooks.ListHooks(ctx, webmw.ViewerID(ctx), owner, name)
	if err != nil {
		return h.serviceError(c, err)
	}
	rows := make([]view.HookRowVM, 0, len(list))
	for i := range list {
		rows = append(rows, hookRow(owner, name, list[i]))
	}
	vm := view.HookListVM{
		Chrome:       h.view.Chrome(c, "Webhooks"),
		Nav:          h.nav(c, route.RepoHooks(owner, name)),
		RepoFullName: owner + "/" + name,
		NewURL:       route.RepoHookNew(owner, name),
		Hooks:        rows,
		Empty:        len(rows) == 0,
	}
	return h.render.Page(c, "settings/hooks", vm)
}

// NewHook renders the blank create form, prefilled with the defaults a new hook
// takes: JSON content type, active, and the push event subscribed.
func (h *Handlers) NewHook(c *mizu.Ctx) error {
	owner, name := h.owner(c), h.name(c)
	f := hookForm{
		contentType: "json",
		active:      true,
		events:      []string{domain.EventPush},
	}
	vm := h.formVM(c, formContext{
		title:     "Add webhook",
		action:    route.RepoHooks(owner, name),
		isNew:     true,
		form:      f,
		hasSecret: false,
	})
	return h.render.Page(c, "settings/hook_form", vm)
}

// CreateHook validates and registers a webhook. A validation failure re-renders
// the filled form with an inline message rather than an error page, so the viewer
// fixes the one bad field without retyping the rest. On success it flashes and
// redirects to the new hook's edit page, where the delivery history will appear.
func (h *Handlers) CreateHook(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	f := parseHookForm(c)
	in := domain.HookInput{
		URL:         f.payloadURL,
		ContentType: f.contentType,
		InsecureSSL: f.insecureSSL,
		Active:      &f.active,
		Events:      f.eventList(),
	}
	if f.secret != "" {
		in.Secret = &f.secret
	}
	hook, err := h.hooks.CreateHook(ctx, webmw.ViewerID(ctx), owner, name, in)
	if errors.Is(err, domain.ErrValidation) {
		vm := h.formVM(c, formContext{
			title:     "Add webhook",
			action:    route.RepoHooks(owner, name),
			isNew:     true,
			form:      f,
			formError: "We could not add that webhook. Check that the payload URL is a full http or https address.",
		})
		return h.render.Page(c, "settings/hook_form", vm)
	}
	if err != nil {
		return h.serviceError(c, err)
	}
	h.flash.Add(c, "success", "Webhook added.")
	return h.redirect(c, route.RepoHook(owner, name, hook.ID))
}

// EditHook renders the form prefilled from an existing hook, with its recent
// delivery history below. A history read that fails degrades to no history rather
// than failing the whole page, so a transient store hiccup does not lock the
// viewer out of editing the hook.
func (h *Handlers) EditHook(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	hookID, ok := parseID(c, "hook")
	if !ok {
		return h.notFound(c)
	}
	hook, err := h.hooks.GetHook(ctx, webmw.ViewerID(ctx), owner, name, hookID)
	if errors.Is(err, domain.ErrHookNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return h.serviceError(c, err)
	}
	vm := h.formVM(c, formContext{
		title:      hook.Config.URL,
		action:     route.RepoHook(owner, name, hookID),
		deleteURL:  route.RepoHookDelete(owner, name, hookID),
		form:       hookFormFromHook(hook),
		hasSecret:  hook.Config.HasSecret,
		deliveries: h.deliveryRows(c, owner, name, hookID),
	})
	return h.render.Page(c, "settings/hook_form", vm)
}

// UpdateHook applies the submitted form to an existing hook. The secret is
// write-only: a blank field keeps the stored secret, a typed value replaces it,
// and the explicit remove control clears it. A validation failure re-renders the
// filled form with its history intact.
func (h *Handlers) UpdateHook(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	viewerPK := webmw.ViewerID(ctx)
	hookID, ok := parseID(c, "hook")
	if !ok {
		return h.notFound(c)
	}
	f := parseHookForm(c)
	events := f.eventList()
	p := domain.HookPatch{
		URL:         &f.payloadURL,
		ContentType: &f.contentType,
		InsecureSSL: &f.insecureSSL,
		Active:      &f.active,
		Events:      &events,
	}
	switch {
	case f.clearSecret:
		empty := ""
		p.Secret = &empty
	case f.secret != "":
		p.Secret = &f.secret
	}
	_, err := h.hooks.UpdateHook(ctx, viewerPK, owner, name, hookID, p)
	if errors.Is(err, domain.ErrHookNotFound) {
		return h.notFound(c)
	}
	if errors.Is(err, domain.ErrValidation) {
		// The save was rejected, so the stored hook is unchanged: re-read it for the
		// accurate secret state and the delivery history, and re-render the filled
		// form with the inline message.
		hasSecret := false
		if existing, gerr := h.hooks.GetHook(ctx, viewerPK, owner, name, hookID); gerr == nil {
			hasSecret = existing.Config.HasSecret
		}
		vm := h.formVM(c, formContext{
			title:      f.payloadURL,
			action:     route.RepoHook(owner, name, hookID),
			deleteURL:  route.RepoHookDelete(owner, name, hookID),
			form:       f,
			hasSecret:  hasSecret,
			formError:  "We could not update that webhook. Check that the payload URL is a full http or https address.",
			deliveries: h.deliveryRows(c, owner, name, hookID),
		})
		return h.render.Page(c, "settings/hook_form", vm)
	}
	if err != nil {
		return h.serviceError(c, err)
	}
	h.flash.Add(c, "success", "Webhook updated.")
	return h.redirect(c, route.RepoHook(owner, name, hookID))
}

// DeleteHook removes a webhook and returns to the list. Deleting is a POST behind
// the CSRF guard, never a GET, so a crawler or a prefetch cannot remove a hook.
func (h *Handlers) DeleteHook(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	hookID, ok := parseID(c, "hook")
	if !ok {
		return h.notFound(c)
	}
	err := h.hooks.DeleteHook(ctx, webmw.ViewerID(ctx), owner, name, hookID)
	if errors.Is(err, domain.ErrHookNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return h.serviceError(c, err)
	}
	h.flash.Add(c, "success", "Webhook deleted.")
	return h.redirect(c, route.RepoHooks(owner, name))
}

// Delivery renders one recorded delivery in full: the request and response
// headers and bodies the worker stored, so an integrator can see exactly what
// Githome sent and what the endpoint answered.
func (h *Handlers) Delivery(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	hookID, ok := parseID(c, "hook")
	if !ok {
		return h.notFound(c)
	}
	deliveryID, ok := parseID(c, "delivery")
	if !ok {
		return h.notFound(c)
	}
	d, err := h.hooks.GetHookDelivery(ctx, webmw.ViewerID(ctx), owner, name, hookID, deliveryID)
	if errors.Is(err, domain.ErrHookNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return h.serviceError(c, err)
	}
	vm := view.HookDeliveryDetailVM{
		Chrome:          h.view.Chrome(c, "Delivery "+d.GUID),
		Nav:             h.nav(c, route.RepoHooks(owner, name)),
		BackURL:         route.RepoHook(owner, name, hookID),
		Row:             deliveryRow(owner, name, hookID, *d),
		RequestHeaders:  headerPairs(d.Request),
		RequestBody:     bodyOf(d.Request),
		ResponseHeaders: headerPairs(d.Response),
		ResponseBody:    bodyOf(d.Response),
	}
	return h.render.Page(c, "settings/hook_delivery", vm)
}

// Redeliver replays a recorded delivery and returns to the hook, where the new
// attempt joins the history.
func (h *Handlers) Redeliver(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	hookID, ok := parseID(c, "hook")
	if !ok {
		return h.notFound(c)
	}
	deliveryID, ok := parseID(c, "delivery")
	if !ok {
		return h.notFound(c)
	}
	err := h.hooks.RedeliverHookDelivery(ctx, webmw.ViewerID(ctx), owner, name, hookID, deliveryID)
	if errors.Is(err, domain.ErrHookNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return h.serviceError(c, err)
	}
	h.flash.Add(c, "info", "Redelivery queued. It will arrive in the history shortly.")
	return h.redirect(c, route.RepoHook(owner, name, hookID))
}

// hookForm is the submitted webhook form, parsed once so the create and edit
// paths share the same field reading and so a validation re-render keeps exactly
// what the viewer typed.
type hookForm struct {
	payloadURL   string
	contentType  string
	secret       string
	clearSecret  bool
	insecureSSL  bool
	active       bool
	subscribeAll bool
	events       []string
}

// eventList renders the subscription the service stores: the wildcard when the
// viewer chose to be sent everything, otherwise the individual events they
// checked. An empty list defaults to push in the service, matching GitHub.
func (f hookForm) eventList() []string {
	if f.subscribeAll {
		return []string{"*"}
	}
	return f.events
}

// parseHookForm reads the webhook form. The event subscription is a radio (send
// everything, or choose individual events) plus the individual checkboxes; an
// unknown event name is dropped against the closed catalog so a forged post
// cannot subscribe to an event Githome never emits.
func parseHookForm(c *mizu.Ctx) hookForm {
	form, err := c.Form()
	if err != nil {
		return hookForm{}
	}
	f := hookForm{
		payloadURL:   form.Get("payload_url"),
		contentType:  form.Get("content_type"),
		secret:       form.Get("secret"),
		clearSecret:  form.Get("clear_secret") != "",
		insecureSSL:  form.Get("insecure_ssl") != "",
		active:       form.Get("active") != "",
		subscribeAll: form.Get("subscribe") == "all",
	}
	valid := map[string]bool{}
	for _, name := range view.HookEventNames() {
		valid[name] = true
	}
	for _, e := range form["events"] {
		if valid[e] {
			f.events = append(f.events, e)
		}
	}
	return f
}

// hookFormFromHook fills the form from an existing hook for the edit page. The
// secret is never read back; HasSecret on the view model carries whether one is
// set, so the field stays blank.
func hookFormFromHook(hook *domain.Hook) hookForm {
	f := hookForm{
		payloadURL:   hook.Config.URL,
		contentType:  hook.Config.ContentType,
		insecureSSL:  hook.Config.InsecureSSL,
		active:       hook.Active,
		subscribeAll: view.HookSubscribesAll(hook.Events),
		events:       hook.Events,
	}
	return f
}

// formContext gathers what the shared create-and-edit form needs that is not the
// submitted values themselves, so formVM has one parameter rather than eight.
type formContext struct {
	title      string
	action     string
	isNew      bool
	deleteURL  string
	form       hookForm
	hasSecret  bool
	formError  string
	deliveries []view.HookDeliveryRowVM
}

// formVM assembles the shared webhook form view model from the submitted values
// and the surrounding context.
func (h *Handlers) formVM(c *mizu.Ctx, fc formContext) view.HookFormVM {
	owner, name := h.owner(c), h.name(c)
	return view.HookFormVM{
		Chrome:       h.view.Chrome(c, fc.title),
		Nav:          h.nav(c, route.RepoHooks(owner, name)),
		Title:        fc.title,
		Action:       fc.action,
		IsNew:        fc.isNew,
		DeleteAction: fc.deleteURL,
		FormError:    fc.formError,
		PayloadURL:   fc.form.payloadURL,
		ContentType:  fc.form.contentType,
		HasSecret:    fc.hasSecret,
		InsecureSSL:  fc.form.insecureSSL,
		Active:       fc.form.active,
		ContentTypes: view.HookContentTypeOptions(fc.form.contentType),
		Events:       view.HookEventChoices(fc.form.events),
		Everything:   fc.form.subscribeAll,
		Deliveries:   fc.deliveries,
	}
}

// deliveryRows reads a hook's recent deliveries for the edit page, degrading to
// an empty history (with a logged warning) rather than failing the page when the
// read errors.
func (h *Handlers) deliveryRows(c *mizu.Ctx, owner, name string, hookID int64) []view.HookDeliveryRowVM {
	ctx := c.Context()
	list, err := h.hooks.ListHookDeliveries(ctx, webmw.ViewerID(ctx), owner, name, hookID, deliveryHistoryLimit)
	if err != nil {
		h.log.WarnContext(ctx, "fe: hook delivery history unavailable", "err", err, "owner", owner, "repo", name, "hook", hookID)
		return nil
	}
	rows := make([]view.HookDeliveryRowVM, 0, len(list))
	for i := range list {
		rows = append(rows, deliveryRow(owner, name, hookID, list[i]))
	}
	return rows
}

// hookRow maps a hook to its list row, reading its last-delivery status for the
// at-a-glance health glyph.
func hookRow(owner, name string, hook domain.Hook) view.HookRowVM {
	status := "unused"
	if hook.LastResponse != nil && hook.LastResponse.Status != "" {
		status = hook.LastResponse.Status
	}
	icon, kind := view.HookStatusGlyph(status)
	return view.HookRowVM{
		URL:         route.RepoHook(owner, name, hook.ID),
		Target:      hook.Config.URL,
		Active:      hook.Active,
		Events:      view.EventsSummary(hook.Events),
		StatusIcon:  icon,
		StatusKind:  kind,
		StatusLabel: status,
	}
}

// deliveryRow maps a delivery to its history row and to the header of its detail
// page, with both a human and a machine timestamp for the relative-time element.
func deliveryRow(owner, name string, hookID int64, d domain.HookDelivery) view.HookDeliveryRowVM {
	icon, kind := view.HookStatusGlyph(d.Status)
	return view.HookDeliveryRowVM{
		URL:             route.RepoHookDelivery(owner, name, hookID, d.ID),
		GUID:            d.GUID,
		Event:           d.Event,
		StatusIcon:      icon,
		StatusKind:      kind,
		StatusLabel:     d.Status,
		Redelivery:      d.Redelivery,
		DeliveredAt:     d.DeliveredAt.UTC().Format("Jan 2, 2006, 3:04 PM"),
		DeliveredISO:    d.DeliveredAt.UTC().Format(time.RFC3339),
		RedeliverAction: route.RepoHookRedeliver(owner, name, hookID, d.ID),
	}
}

// headerPairs turns a delivery half's header map into the ordered pairs the
// template prints, sorted by name so the view is stable (Go randomizes map
// order). A nil half (an attempt that never connected) yields no rows.
func headerPairs(half *domain.HookDeliveryHTTP) []view.HeaderKV {
	if half == nil {
		return nil
	}
	out := make([]view.HeaderKV, 0, len(half.Headers))
	for k, v := range half.Headers {
		out = append(out, view.HeaderKV{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// bodyOf returns a delivery half's body, the empty string for a nil half.
func bodyOf(half *domain.HookDeliveryHTTP) string {
	if half == nil {
		return ""
	}
	return half.Body
}

// redirect sends the browser to location with 303 See Other so a reload after a
// post re-fetches with GET rather than re-submitting the form.
func (h *Handlers) redirect(c *mizu.Ctx, location string) error {
	return c.Redirect(http.StatusSeeOther, location)
}

// serviceError maps a service error the Resolve gate should have prevented. A
// forbidden slips to the same 404 the gate renders (defense in depth against a
// race between the read gate and the write authority); anything else is a real
// server error.
func (h *Handlers) serviceError(c *mizu.Ctx, err error) error {
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrRepoNotFound) {
		return h.notFound(c)
	}
	return err
}
