package rest

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
)

// deviceCodeGrant is the OAuth grant_type gh sends when polling the token
// endpoint during the device flow.
const deviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"

// handleOAuthDiscovery serves GET /.well-known/oauth-authorization-server
// (RFC 8414). git-credential-oauth and GCM read this document to locate the
// authorize and token endpoints without hardcoding paths. The document must be
// served even when Auth is nil so unauthenticated deploys still expose the
// discovery endpoint for tools that probe it before attempting auth.
func handleOAuthDiscovery(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		base := ""
		if d.URLs != nil {
			base = d.URLs.HTMLBase()
		}
		doc := struct {
			Issuer                            string   `json:"issuer"`
			AuthorizationEndpoint             string   `json:"authorization_endpoint"`
			TokenEndpoint                     string   `json:"token_endpoint"`
			DeviceAuthorizationEndpoint       string   `json:"device_authorization_endpoint"`
			GrantTypesSupported               []string `json:"grant_types_supported"`
			TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
		}{
			Issuer:                            base,
			AuthorizationEndpoint:             base + "/login/oauth/authorize",
			TokenEndpoint:                     base + "/login/oauth/access_token",
			DeviceAuthorizationEndpoint:       base + "/login/device/code",
			GrantTypesSupported:               []string{"authorization_code", deviceCodeGrant},
			TokenEndpointAuthMethodsSupported: []string{"client_secret_post"},
		}
		writeJSON(c.Writer(), http.StatusOK, doc)
		return nil
	}
}

// mountOAuth registers the device-flow endpoints. They live at the bare root,
// outside /api/v3 and outside the API version, media-type, and auth middleware,
// exactly like github.com (the token endpoint authenticates by client_id and
// device_code in the body, not by a bearer credential).
func mountOAuth(root *mizu.Router, svc *auth.Service) {
	root.Post("/login/device/code", handleDeviceCode(svc))
	root.Post("/login/oauth/access_token", handleAccessToken(svc))
}

// handleDeviceCode serves POST /login/device/code: it opens a device-flow
// session and returns the device and user codes.
func handleDeviceCode(svc *auth.Service) mizu.Handler {
	return func(c *mizu.Ctx) error {
		r := c.Request()
		_ = r.ParseForm()
		res, err := svc.RequestDeviceCode(r.Context(), r.PostForm.Get("client_id"), r.PostForm.Get("scope"))
		switch {
		case errors.Is(err, auth.ErrUnknownClient):
			renderOAuth(c, http.StatusOK, oauthError{Err: "incorrect_client_credentials", Desc: "The client_id is not registered."})
			return nil
		case errors.Is(err, auth.ErrDeviceFlowDisabled):
			renderOAuth(c, http.StatusOK, oauthError{Err: "device_flow_disabled", Desc: "Device flow is not enabled for this app."})
			return nil
		case err != nil:
			return err
		}
		renderOAuth(c, http.StatusOK, oauthDeviceCode{
			DeviceCode:      res.DeviceCode,
			UserCode:        res.UserCode,
			VerificationURI: res.VerificationURI,
			ExpiresIn:       res.ExpiresIn,
			Interval:        res.Interval,
		})
		return nil
	}
}

// handleAccessToken serves POST /login/oauth/access_token. Supports both the
// device-code grant and the authorization-code grant (web flow). The response
// is always HTTP 200 with either the token or an OAuth error body, matching
// GitHub's behavior.
func handleAccessToken(svc *auth.Service) mizu.Handler {
	return func(c *mizu.Ctx) error {
		r := c.Request()
		_ = r.ParseForm()
		switch grant := r.PostForm.Get("grant_type"); grant {
		case deviceCodeGrant:
			outcome, err := svc.PollDeviceToken(r.Context(), r.PostForm.Get("client_id"), r.PostForm.Get("device_code"))
			if err != nil {
				return err
			}
			if outcome.Token != nil {
				renderOAuth(c, http.StatusOK, oauthToken{
					AccessToken: outcome.Token.AccessToken,
					TokenType:   outcome.Token.TokenType,
					Scope:       outcome.Token.Scope,
				})
				return nil
			}
			renderOAuth(c, http.StatusOK, oauthError{Err: outcome.Error, Desc: outcome.ErrorDescription, Interval: outcome.Interval})
			return nil
		case "authorization_code":
			tok, err := svc.ExchangeAuthCode(r.Context(),
				r.PostForm.Get("client_id"),
				r.PostForm.Get("client_secret"),
				r.PostForm.Get("code"),
				r.PostForm.Get("redirect_uri"),
			)
			if errors.Is(err, auth.ErrUnknownClient) || errors.Is(err, auth.ErrInvalidClientSecret) {
				renderOAuth(c, http.StatusOK, oauthError{Err: "incorrect_client_credentials", Desc: "The client_id and/or client_secret passed are incorrect."})
				return nil
			}
			if errors.Is(err, auth.ErrInvalidRedirectURI) {
				renderOAuth(c, http.StatusOK, oauthError{Err: "redirect_uri_mismatch", Desc: "The redirect_uri MUST match the registered callback URL for this application."})
				return nil
			}
			if errors.Is(err, auth.ErrInvalidCode) {
				renderOAuth(c, http.StatusOK, oauthError{Err: "bad_verification_code", Desc: "The code passed is incorrect or expired."})
				return nil
			}
			if err != nil {
				return err
			}
			renderOAuth(c, http.StatusOK, oauthToken{
				AccessToken: tok.AccessToken,
				TokenType:   tok.TokenType,
				Scope:       tok.Scope,
			})
			return nil
		default:
			renderOAuth(c, http.StatusOK, oauthError{Err: "unsupported_grant_type", Desc: "Supported grant types: device_code, authorization_code."})
			return nil
		}
	}
}

// The OAuth endpoints answer in JSON when the client sends Accept:
// application/json (gh does) and otherwise in the form-encoded body GitHub
// returns by default. Each response type renders both shapes.

type oauthDeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

func (o oauthDeviceCode) form() url.Values {
	return url.Values{
		"device_code":      {o.DeviceCode},
		"user_code":        {o.UserCode},
		"verification_uri": {o.VerificationURI},
		"expires_in":       {strconv.Itoa(o.ExpiresIn)},
		"interval":         {strconv.Itoa(o.Interval)},
	}
}

type oauthToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

func (o oauthToken) form() url.Values {
	return url.Values{
		"access_token": {o.AccessToken},
		"token_type":   {o.TokenType},
		"scope":        {o.Scope},
	}
}

type oauthError struct {
	Err      string `json:"error"`
	Desc     string `json:"error_description,omitempty"`
	Interval int    `json:"interval,omitempty"`
}

func (o oauthError) form() url.Values {
	v := url.Values{"error": {o.Err}}
	if o.Desc != "" {
		v.Set("error_description", o.Desc)
	}
	if o.Interval != 0 {
		v.Set("interval", strconv.Itoa(o.Interval))
	}
	return v
}

// oauthBody is satisfied by every OAuth response type so renderOAuth can encode
// either shape.
type oauthBody interface{ form() url.Values }

func renderOAuth(c *mizu.Ctx, status int, body oauthBody) {
	if wantsJSON(c.Request()) {
		writeJSON(c.Writer(), status, body)
		return
	}
	w := c.Writer()
	w.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body.form().Encode()))
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
