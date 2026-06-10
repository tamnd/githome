package auth

// device.go holds the device-flow approval page at /login/device, the
// verification_uri the device-code response points gh and other CLI clients at.
// The page is gated to a signed-in viewer: anonymous requests bounce to /login
// with a return_to back here, since the approval binds the device's token to
// the approving account. The form asks for the user code the device showed,
// and the POST approves or denies the pending device session through the auth
// service. See spec doc 03 section 5.2.

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// deviceVM is the view model for the device approval page.
type deviceVM struct {
	view.Chrome
	Action   string
	UserCode string
	Error    string
	Done     string // "approved" or "denied" after a successful submit
}

// DeviceForm serves GET /login/device: the form asking for the user code a
// device displayed. An anonymous viewer is sent to sign in first, then bounced
// back here with any user_code prefill preserved.
func (h *OAuthHandlers) DeviceForm(c *mizu.Ctx) error {
	if view.ViewerFrom(c.Context()) == nil {
		returnTo := "/login/device"
		if q := c.Request().URL.RawQuery; q != "" {
			returnTo += "?" + q
		}
		return c.Redirect(http.StatusSeeOther, "/login?return_to="+url.QueryEscape(returnTo))
	}
	return h.render.Page(c, "auth/device", deviceVM{
		Chrome:   h.view.Chrome(c, "Device activation"),
		Action:   "/login/device",
		UserCode: c.Request().URL.Query().Get("user_code"),
	})
}

// DeviceSubmit serves POST /login/device: it approves or denies the pending
// device session behind the submitted user code. Approval binds the session to
// the signed-in viewer, so the device's next token poll mints a token for that
// account. An unknown or expired code re-renders the form with an error rather
// than confirming anything about other sessions.
func (h *OAuthHandlers) DeviceSubmit(c *mizu.Ctx) error {
	if view.ViewerFrom(c.Context()) == nil {
		return c.Redirect(http.StatusSeeOther, "/login?return_to="+url.QueryEscape("/login/device"))
	}
	if err := c.Request().ParseForm(); err != nil {
		return err
	}
	userCode := strings.TrimSpace(c.Request().FormValue("user_code"))
	action := c.Request().FormValue("action")

	vm := deviceVM{
		Chrome:   h.view.Chrome(c, "Device activation"),
		Action:   "/login/device",
		UserCode: userCode,
	}
	if userCode == "" {
		vm.Error = "Enter the code displayed on your device."
		return h.render.Page(c, "auth/device", vm)
	}

	var err error
	if action == "deny" {
		err = h.svc.DenyDeviceCode(c.Context(), userCode)
	} else {
		err = h.svc.ApproveDeviceCode(c.Context(), userCode, webmw.ViewerID(c.Context()))
	}
	if err != nil {
		// An unknown and an expired code answer identically, so the page never
		// confirms whether a guessed code exists.
		vm.Error = "The code is invalid or has expired. Check the code on your device and try again."
		return h.render.Page(c, "auth/device", vm)
	}

	vm.UserCode = ""
	if action == "deny" {
		vm.Done = "denied"
	} else {
		vm.Done = "approved"
	}
	return h.render.Page(c, "auth/device", vm)
}
