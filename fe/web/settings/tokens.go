package settings

// tokens.go holds the personal access tokens section at /settings/tokens: the
// list of the viewer's classic tokens, the form that mints a new one, and the
// delete that revokes one. The mint response renders the plaintext directly on
// the page rather than staging it in a flash cookie, so the secret is shown
// exactly once and never written anywhere. Without a token service the page
// falls back to the honest-absence stub it showed before tokens were backed.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// PAT is the displayable summary of one personal access token. It carries
// everything the page shows and nothing that authenticates.
type PAT struct {
	ID         int64
	Note       string
	Scopes     string
	LastEight  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// TokenService is the slice of the auth service the tokens page uses: mint,
// list, delete. cmd/githome adapts *auth.Service to it; the narrow interface
// keeps the web front off the auth package and the handler testable with a
// fake.
type TokenService interface {
	CreatePAT(ctx context.Context, userPK int64, note string, scopes []string) (string, error)
	ListPATs(ctx context.Context, userPK int64) ([]PAT, error)
	DeletePAT(ctx context.Context, userPK, id int64) error
}

// tokenScopeOptions are the classic scopes the mint form offers. The list is
// the working subset GitHub's form leads with; anything else can ride the API
// once scope management grows past the page.
var tokenScopeOptions = []tokenScopeOption{
	{"repo", "Full control of private repositories"},
	{"workflow", "Update GitHub Actions workflows"},
	{"gist", "Create and update gists"},
	{"notifications", "Access notifications"},
	{"user", "Read and update profile data"},
	{"delete_repo", "Delete repositories"},
	{"admin:repo_hook", "Manage repository webhooks"},
	{"admin:public_key", "Manage SSH keys"},
}

type tokenScopeOption struct {
	Name        string
	Description string
}

// tokensVM is the view model for the tokens page.
type tokensVM struct {
	view.Chrome
	Nav      view.SettingsNav
	Backed   bool
	Action   string
	Tokens   []tokenItemVM
	Scopes   []tokenScopeOption
	NewToken string // plaintext of a token minted by this response, shown once
	Error    string
}

// tokenItemVM is one token row, with the delete target precomputed so the
// template stays free of URL assembly, and the nullable last-used time
// flattened into a value the relativeTime template func can take.
type tokenItemVM struct {
	PAT
	DeleteURL string
	Used      bool
	LastUsed  time.Time
}

// Tokens renders the personal access tokens page: the viewer's live tokens and
// the mint form. With no token service wired it renders the honest-absence
// stub instead.
func (h *Handlers) Tokens(c *mizu.Ctx) error {
	v, ok := h.gate(c)
	if !ok {
		return h.notFound(c)
	}
	return h.renderTokens(c, v, "", "")
}

// NewToken renders the mint-a-token form at /settings/tokens/new, the dedicated
// page github.com links to for creating a classic token. It renders the same
// tokens page the list lives on, whose mint form is the focus here; with no
// token service wired it falls back to the honest-absence stub.
func (h *Handlers) NewToken(c *mizu.Ctx) error {
	v, ok := h.gate(c)
	if !ok {
		return h.notFound(c)
	}
	return h.renderTokens(c, v, "", "")
}

// CreateToken mints a new token from the form's note and scopes and re-renders
// the page with the one-time plaintext. It renders rather than redirects: the
// secret must not survive the response, so it never enters a cookie.
func (h *Handlers) CreateToken(c *mizu.Ctx) error {
	v, ok := h.gate(c)
	if !ok || h.tokens == nil {
		return h.notFound(c)
	}
	form, err := c.Form()
	if err != nil {
		return h.renderTokens(c, v, "", "Could not read the form. Please try again.")
	}
	note := strings.TrimSpace(form.Get("note"))
	if note == "" {
		return h.renderTokens(c, v, "", "Give the token a note so you can tell it apart later.")
	}
	scopes := form["scopes"]
	pk := webmw.ViewerID(c.Context())
	plain, err := h.tokens.CreatePAT(c.Context(), pk, note, scopes)
	if err != nil {
		h.log.Error("settings tokens: create", "err", err)
		return h.renderTokens(c, v, "", "Could not create the token. Please try again.")
	}
	return h.renderTokens(c, v, plain, "")
}

// DeleteToken revokes one of the viewer's tokens and redirects back to the
// list. Deleting a token the viewer does not have lands on the same flash as a
// double-submit: the row is gone either way.
func (h *Handlers) DeleteToken(c *mizu.Ctx) error {
	if _, ok := h.gate(c); !ok || h.tokens == nil {
		return h.notFound(c)
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return h.notFound(c)
	}
	pk := webmw.ViewerID(c.Context())
	if err := h.tokens.DeletePAT(c.Context(), pk, id); err != nil {
		h.flash.Add(c, "error", "The token was not found. It may already be deleted.")
		return redirect(c, route.SettingsTokens())
	}
	h.flash.Add(c, "success", "Token deleted. Anything still using it will stop authenticating.")
	return redirect(c, route.SettingsTokens())
}

// renderTokens builds the page model and renders it. newToken carries the
// one-time plaintext of a token this response minted, errMsg an inline form
// error; both are empty on a plain GET.
func (h *Handlers) renderTokens(c *mizu.Ctx, v *view.Viewer, newToken, errMsg string) error {
	vm := tokensVM{
		Chrome:   h.view.Chrome(c, "Personal access tokens"),
		Nav:      h.nav(v, route.SettingsTokens()),
		Action:   route.SettingsTokens(),
		Scopes:   tokenScopeOptions,
		NewToken: newToken,
		Error:    errMsg,
	}
	if h.tokens == nil {
		return h.render.Page(c, "settings/tokens", vm)
	}
	vm.Backed = true
	pats, err := h.tokens.ListPATs(c.Context(), webmw.ViewerID(c.Context()))
	if err != nil {
		h.log.Error("settings tokens: list", "err", err)
		return h.notFound(c)
	}
	vm.Tokens = make([]tokenItemVM, 0, len(pats))
	for _, p := range pats {
		item := tokenItemVM{PAT: p, DeleteURL: route.SettingsTokenDelete(p.ID)}
		if p.LastUsedAt != nil {
			item.Used, item.LastUsed = true, *p.LastUsedAt
		}
		vm.Tokens = append(vm.Tokens, item)
	}
	return h.render.Page(c, "settings/tokens", vm)
}
