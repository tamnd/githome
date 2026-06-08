package pulls

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
)

// writes.go holds the shared plumbing the mutating pull-request handlers reuse:
// form reading, the redirect-after-post that keeps the no-JS flow on a clean GET,
// and the mapping from a domain error to a response. Every mutation re-authorizes
// through the service, so these helpers never decide permission; they translate
// the outcome. They mirror the issues surface so a comment posts and a write fails
// the same way on both. See implementation/09 section 3.

// formString reads a trimmed form value. A parse failure yields the empty string,
// which the caller treats as a missing field.
func formString(c *mizu.Ctx, key string) string {
	form, err := c.Form()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(form.Get(key))
}

// formRaw reads a form value without trimming, for a body or a commit message
// where interior whitespace is intentional (the ends are trimmed only to test
// emptiness, not stored trimmed).
func formRaw(c *mizu.Ctx, key string) string {
	form, err := c.Form()
	if err != nil {
		return ""
	}
	return form.Get(key)
}

// isBlank reports whether a body is empty once trimmed, the gate a comment submit
// checks before it reaches the service.
func isBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// redirect sends the browser to location with 303 See Other, the correct status
// after a successful POST so a reload re-fetches with GET rather than re-submitting
// the form.
func redirect(c *mizu.Ctx, location string) error {
	return c.Redirect(http.StatusSeeOther, location)
}

// writeError maps a domain mutation error to a response. A not-found resource is
// the soft 404; a forbidden action on a visible repo is the themed 403 (the repo's
// existence is already public here, so a 404 would mislead); anything else is
// returned for the recover layer to render a 500.
func (h *Handlers) writeError(c *mizu.Ctx, err error) error {
	switch {
	case isNotFound(err) || errors.Is(err, domain.ErrCommentNotFound):
		return h.notFound(c)
	case errors.Is(err, domain.ErrForbidden):
		return h.render.Forbidden(c, h.chrome(c, ""))
	default:
		return err
	}
}

// isValidation reports whether err is the domain validation sentinel the form
// handlers echo back inline rather than turning into an error page.
func isValidation(err error) bool {
	return errors.Is(err, domain.ErrValidation)
}
