package rest

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/store"
)

// gistCreateBody is the POST /gists request.
type gistCreateBody struct {
	Description string                     `json:"description"`
	Public      bool                       `json:"public"`
	Files       map[string]gistFilePayload `json:"files"`
}

// gistUpdateBody is the PATCH /gists/{gist_id} request.
type gistUpdateBody struct {
	Description *string                     `json:"description"`
	Files       map[string]*gistFilePayload `json:"files"`
}

// gistFilePayload is the files map value in create/update requests.
type gistFilePayload struct {
	Content  *string `json:"content"`
	Filename *string `json:"filename"`
}

// gistCommentBody is the POST /gists/{gist_id}/comments request.
type gistCommentBody struct {
	Body string `json:"body"`
}

// mountGists registers the Gist API endpoints on r.
func mountGists(r *mizu.Router, d Deps) {
	if d.Gists == nil {
		return
	}
	r.Get("/gists", handleGistList(d))
	r.Post("/gists", handleGistCreate(d))
	r.Get("/gists/public", handleGistListPublic(d))
	r.Get("/gists/starred", handleGistListStarred(d))
	r.Get("/gists/{gist_id}", handleGistGet(d))
	r.Patch("/gists/{gist_id}", handleGistUpdate(d))
	r.Delete("/gists/{gist_id}", handleGistDelete(d))
	r.Post("/gists/{gist_id}/forks", handleGistFork(d))
	r.Get("/gists/{gist_id}/commits", handleGistCommits(d))
	r.Put("/gists/{gist_id}/star", handleGistStar(d))
	r.Delete("/gists/{gist_id}/star", handleGistUnstar(d))
	r.Get("/gists/{gist_id}/star", handleGistIsStarred(d))
	r.Get("/gists/{gist_id}/comments", handleGistCommentsList(d))
	r.Post("/gists/{gist_id}/comments", handleGistCommentCreate(d))
}

// handleUserGists serves GET /users/{username}/gists.
func handleUserGists(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		username := c.Param("username")
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		gists, total, err := d.Gists.ListUserGists(c.Request().Context(), username, actor.UserID, page.Page, page.PerPage)
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		return writeGists(c, d, gists)
	}
}

func handleGistList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		gists, total, err := d.Gists.ListAuthUserGists(c.Request().Context(), actor.UserID, page.Page, page.PerPage)
		if err != nil {
			return err
		}
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		return writeGists(c, d, gists)
	}
}

func handleGistCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		var body gistCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if len(body.Files) == 0 {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Gist", Field: "files", Code: "missing_field"}))
			return nil
		}
		files := make(map[string]string, len(body.Files))
		for fn, f := range body.Files {
			if f.Content != nil {
				files[fn] = *f.Content
			}
		}
		g, err := d.Gists.CreateGist(c.Request().Context(), actor.UserID, domain.GistInput{
			Description: body.Description,
			Public:      body.Public,
			Files:       files,
		})
		if err != nil {
			return err
		}
		out, err := presentGist(c.Request().Context(), d, g, 0)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, out)
		return nil
	}
}

func handleGistListPublic(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		gists, total, err := d.Gists.ListPublicGists(c.Request().Context(), page.Page, page.PerPage)
		if err != nil {
			return err
		}
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		return writeGists(c, d, gists)
	}
}

func handleGistListStarred(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		gists, total, err := d.Gists.ListStarredGists(c.Request().Context(), actor.UserID, page.Page, page.PerPage)
		if err != nil {
			return err
		}
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		return writeGists(c, d, gists)
	}
}

func handleGistGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		gistID := c.Param("gist_id")
		g, err := d.Gists.GetGist(c.Request().Context(), gistID, actor.UserID)
		if errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		comments, _ := d.Gists.ListGistComments(c.Request().Context(), gistID, actor.UserID)
		out, err := presentGist(c.Request().Context(), d, g, len(comments))
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

func handleGistUpdate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		gistID := c.Param("gist_id")
		var body gistUpdateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		files := make(map[string]*string, len(body.Files))
		for fn, f := range body.Files {
			if f == nil {
				files[fn] = nil
			} else {
				files[fn] = f.Content
			}
		}
		g, err := d.Gists.UpdateGist(c.Request().Context(), gistID, actor.UserID, domain.GistUpdateInput{
			Description: body.Description,
			Files:       files,
		})
		if errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("You must be the gist owner to update it."))
			return nil
		}
		if err != nil {
			return err
		}
		out, err := presentGist(c.Request().Context(), d, g, 0)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

func handleGistDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		gistID := c.Param("gist_id")
		err := d.Gists.DeleteGist(c.Request().Context(), gistID, actor.UserID)
		if errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("You must be the gist owner to delete it."))
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func handleGistFork(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		gistID := c.Param("gist_id")
		src, err := d.Gists.GetGist(c.Request().Context(), gistID, actor.UserID)
		if errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		files := make(map[string]string, len(src.Files))
		for _, f := range src.Files {
			files[f.Filename] = f.Content
		}
		forked, err := d.Gists.CreateGist(c.Request().Context(), actor.UserID, domain.GistInput{
			Description: src.Description,
			Public:      src.Public,
			Files:       files,
		})
		if err != nil {
			return err
		}
		out, err := presentGist(c.Request().Context(), d, forked, 0)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, out)
		return nil
	}
}

func handleGistCommits(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		gistID := c.Param("gist_id")
		if _, err := d.Gists.GetGist(c.Request().Context(), gistID, actor.UserID); err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

func handleGistStar(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		gistID := c.Param("gist_id")
		if err := d.Gists.StarGist(c.Request().Context(), gistID, actor.UserID); errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		} else if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func handleGistUnstar(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		gistID := c.Param("gist_id")
		if err := d.Gists.UnstarGist(c.Request().Context(), gistID, actor.UserID); errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		} else if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func handleGistIsStarred(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		gistID := c.Param("gist_id")
		starred, err := d.Gists.IsGistStarred(c.Request().Context(), gistID, actor.UserID)
		if errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if starred {
			c.Writer().WriteHeader(http.StatusNoContent)
		} else {
			writeError(c.Writer(), errNotFound())
		}
		return nil
	}
}

func handleGistCommentsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		gistID := c.Param("gist_id")
		comments, err := d.Gists.ListGistComments(c.Request().Context(), gistID, actor.UserID)
		if errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]any, 0, len(comments))
		for i := range comments {
			u, err := d.Users.Viewer(c.Request().Context(), comments[i].UserPK)
			if err != nil {
				continue
			}
			out = append(out, d.URLs.GistComment(&comments[i], u, d.NodeFormat))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

func handleGistCommentCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		if actor.UserID == 0 {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		gistID := c.Param("gist_id")
		var body gistCommentBody
		if !decodeJSON(c, &body) {
			return nil
		}
		comment, err := d.Gists.CreateGistComment(c.Request().Context(), gistID, actor.UserID, body.Body)
		if errors.Is(err, domain.ErrGistNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		u, err := d.Users.Viewer(c.Request().Context(), actor.UserID)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.GistComment(comment, u, d.NodeFormat))
		return nil
	}
}

// presentGist fetches the gist owner as a domain.User and returns the presenter output.
func presentGist(ctx context.Context, d Deps, g *store.GistRow, commentCount int) (any, error) {
	owner, err := d.Users.Viewer(ctx, g.OwnerPK)
	if err != nil {
		return nil, err
	}
	return d.URLs.Gist(g, owner, commentCount, d.NodeFormat), nil
}

// writeGists renders a slice of gists and writes them as a JSON array.
func writeGists(c *mizu.Ctx, d Deps, gists []*store.GistRow) error {
	out := make([]any, 0, len(gists))
	for _, g := range gists {
		v, err := presentGist(c.Request().Context(), d, g, 0)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}
