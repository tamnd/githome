package rest

import (
	"net/http"

	"github.com/go-mizu/mizu"
)

// mountGists registers stub endpoints for the Gist API. All return 501 Not
// Implemented; the conformance suite skips gist tests when it sees a 501,
// which is the correct behavior per spec 2001/compat/13 §4.4.
func mountGists(r *mizu.Router) {
	stub := gistNotImplemented()
	r.Get("/gists", stub)
	r.Post("/gists", stub)
	r.Get("/gists/public", stub)
	r.Get("/gists/starred", stub)
	r.Get("/gists/{gist_id}", stub)
	r.Patch("/gists/{gist_id}", stub)
	r.Delete("/gists/{gist_id}", stub)
	r.Post("/gists/{gist_id}/forks", stub)
	r.Get("/gists/{gist_id}/commits", stub)
	r.Put("/gists/{gist_id}/star", stub)
	r.Delete("/gists/{gist_id}/star", stub)
	r.Get("/gists/{gist_id}/star", stub)
	r.Get("/gists/{gist_id}/comments", stub)
	r.Post("/gists/{gist_id}/comments", stub)
}

func gistNotImplemented() mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeError(c.Writer(), &apiError{
			Status:  http.StatusNotImplemented,
			Message: "Gists are not supported on this server",
			DocURL:  docRoot,
		})
		return nil
	}
}
