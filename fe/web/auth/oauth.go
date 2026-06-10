package auth

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// OAuthService is the narrow slice of the auth service the OAuth authorize and
// device-approval handlers call. *auth.Service satisfies this interface
// directly.
type OAuthService interface {
	GenerateOAuthAuthCode(ctx context.Context, clientID, redirectURI, scope string, userPK int64) (string, error)
	OAuthAppName(ctx context.Context, clientID string) (string, bool)
	ApproveDeviceCode(ctx context.Context, userCode string, userPK int64) error
	DenyDeviceCode(ctx context.Context, userCode string) error
}

// OAuthHandlers holds the OAuth authorize-page handlers.
type OAuthHandlers struct {
	svc    OAuthService
	render *render.Set
	view   *view.Builder
}

// oauthAuthorizeVM is the view model for the OAuth consent page.
type oauthAuthorizeVM struct {
	view.Chrome
	Action      string
	ClientID    string
	RedirectURI string
	Scope       string
	State       string
	AppName     string
	Error       string
}

// NewOAuthHandlers creates the OAuth authorize-page handler set.
func NewOAuthHandlers(svc OAuthService, r *render.Set, v *view.Builder) *OAuthHandlers {
	return &OAuthHandlers{svc: svc, render: r, view: v}
}

// AuthorizeForm serves GET /login/oauth/authorize. If the viewer is not logged
// in, it redirects to the sign-in page with a return_to pointing back here.
func (h *OAuthHandlers) AuthorizeForm(c *mizu.Ctx) error {
	q := c.Request().URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	scope := q.Get("scope")
	state := q.Get("state")

	viewer := view.ViewerFrom(c.Context())
	if viewer == nil {
		// Not logged in; send to login page with the full authorize URL as return_to.
		returnTo := "/login/oauth/authorize?" + c.Request().URL.RawQuery
		return c.Redirect(http.StatusSeeOther, "/login?return_to="+url.QueryEscape(returnTo))
	}

	appName, ok := h.svc.OAuthAppName(c.Context(), clientID)
	if !ok {
		vm := oauthAuthorizeVM{
			Chrome: h.view.Chrome(c, "Authorize"),
			Error:  "The client_id is not registered.",
		}
		return h.render.Page(c, "auth/oauth_authorize", vm)
	}

	vm := oauthAuthorizeVM{
		Chrome:      h.view.Chrome(c, "Authorize "+appName),
		Action:      "/login/oauth/authorize",
		ClientID:    clientID,
		RedirectURI: redirectURI,
		Scope:       scope,
		State:       state,
		AppName:     appName,
	}
	return h.render.Page(c, "auth/oauth_authorize", vm)
}

// AuthorizeSubmit serves POST /login/oauth/authorize. Requires a logged-in
// viewer. On approval it generates an auth code and redirects to redirect_uri
// with code and state. On denial it redirects with error=access_denied.
func (h *OAuthHandlers) AuthorizeSubmit(c *mizu.Ctx) error {
	if err := c.Request().ParseForm(); err != nil {
		return err
	}

	userPK := webmw.ViewerID(c.Context())
	if userPK == 0 {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	clientID := c.Request().FormValue("client_id")
	redirectURI := c.Request().FormValue("redirect_uri")
	scope := c.Request().FormValue("scope")
	state := c.Request().FormValue("state")
	action := c.Request().FormValue("action")

	if action == "deny" || redirectURI == "" {
		if redirectURI != "" {
			target := appendOAuthParams(redirectURI, url.Values{
				"error": {"access_denied"},
				"state": {state},
			})
			return c.Redirect(http.StatusSeeOther, target)
		}
		return c.Redirect(http.StatusSeeOther, "/")
	}

	code, err := h.svc.GenerateOAuthAuthCode(c.Context(), clientID, redirectURI, scope, userPK)
	if err != nil {
		return err
	}

	target := appendOAuthParams(redirectURI, url.Values{
		"code":  {code},
		"state": {state},
	})
	return c.Redirect(http.StatusSeeOther, target)
}

// appendOAuthParams adds query params to a redirect URI, preserving any
// existing query string on the URI.
func appendOAuthParams(redirectURI string, params url.Values) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for k, vs := range params {
		for _, v := range vs {
			if v != "" {
				q.Set(k, v)
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
