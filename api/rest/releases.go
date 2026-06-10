package rest

import (
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// releaseCreateBody is the POST /releases request.
type releaseCreateBody struct {
	TagName         string  `json:"tag_name"`
	TargetCommitish string  `json:"target_commitish"`
	Name            *string `json:"name"`
	Body            *string `json:"body"`
	Draft           bool    `json:"draft"`
	Prerelease      bool    `json:"prerelease"`
	MakeLatest      string  `json:"make_latest"`
}

// releaseEditBody is the PATCH /releases/{id} request.
type releaseEditBody struct {
	TagName         *string `json:"tag_name"`
	TargetCommitish *string `json:"target_commitish"`
	Name            *string `json:"name"`
	Body            *string `json:"body"`
	Draft           *bool   `json:"draft"`
	Prerelease      *bool   `json:"prerelease"`
	MakeLatest      string  `json:"make_latest"`
}

// assetEditBody is the PATCH /releases/assets/{id} request.
type assetEditBody struct {
	Name  *string `json:"name"`
	Label *string `json:"label"`
}

// handleReleasesList serves GET /repos/{owner}/{repo}/releases.
func handleReleasesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		releases, err := d.Releases.ListReleases(c.Request().Context(), actor.UserID, owner, repo, page.Page, page.PerPage)
		if err != nil {
			return mapReleaseError(c, err)
		}
		// A full page might be the last one; peek at the next page so the
		// rel="next" link only appears when something is actually there.
		hasNext := false
		if len(releases) == page.PerPage {
			peek, err := d.Releases.ListReleases(c.Request().Context(), actor.UserID, owner, repo, page.Page+1, page.PerPage)
			if err != nil {
				return mapReleaseError(c, err)
			}
			hasNext = len(peek) > 0
		}
		out := make([]any, len(releases))
		for i, r := range releases {
			out[i] = d.URLs.Release(owner, repo, r, d.NodeFormat)
		}
		writeLinkHeaderUncounted(c.Writer(), c.Request(), d.URLs, page, hasNext)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleReleaseCreate serves POST /repos/{owner}/{repo}/releases.
func handleReleaseCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		var body releaseCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		in := domain.ReleaseInput{
			TagName:         body.TagName,
			TargetCommitish: body.TargetCommitish,
			Name:            body.Name,
			Body:            body.Body,
			Draft:           body.Draft,
			Prerelease:      body.Prerelease,
			MakeLatest:      body.MakeLatest,
		}
		r, err := d.Releases.CreateRelease(c.Request().Context(), actor.UserID, owner, repo, in)
		if err != nil {
			return mapReleaseError(c, err)
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Release(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReleaseGet serves GET /repos/{owner}/{repo}/releases/{id}.
func handleReleaseGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "release_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		r, err := d.Releases.GetRelease(c.Request().Context(), actor.UserID, owner, repo, id)
		if err != nil {
			return mapReleaseError(c, err)
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Release(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReleaseLatest serves GET /repos/{owner}/{repo}/releases/latest.
func handleReleaseLatest(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		r, err := d.Releases.GetLatestRelease(c.Request().Context(), actor.UserID, owner, repo)
		if err != nil {
			return mapReleaseError(c, err)
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Release(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReleaseByTag serves GET /repos/{owner}/{repo}/releases/tags/{tag}.
func handleReleaseByTag(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		tag := c.Param("tag")
		r, err := d.Releases.GetReleaseByTag(c.Request().Context(), actor.UserID, owner, repo, tag)
		if err != nil {
			return mapReleaseError(c, err)
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Release(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReleaseEdit serves PATCH /repos/{owner}/{repo}/releases/{id}.
func handleReleaseEdit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "release_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body releaseEditBody
		if !decodeJSON(c, &body) {
			return nil
		}
		existing, err := d.Releases.GetRelease(c.Request().Context(), actor.UserID, owner, repo, id)
		if err != nil {
			return mapReleaseError(c, err)
		}
		in := domain.ReleaseInput{
			TagName:         existing.TagName,
			TargetCommitish: existing.TargetCommitish,
			Name:            existing.Name,
			Body:            existing.Body,
			Draft:           existing.Draft,
			Prerelease:      existing.Prerelease,
			MakeLatest:      body.MakeLatest,
		}
		if body.TagName != nil {
			in.TagName = *body.TagName
		}
		if body.TargetCommitish != nil {
			in.TargetCommitish = *body.TargetCommitish
		}
		if body.Name != nil {
			in.Name = body.Name
		}
		if body.Body != nil {
			in.Body = body.Body
		}
		if body.Draft != nil {
			in.Draft = *body.Draft
		}
		if body.Prerelease != nil {
			in.Prerelease = *body.Prerelease
		}
		r, err := d.Releases.UpdateRelease(c.Request().Context(), actor.UserID, owner, repo, id, in)
		if err != nil {
			return mapReleaseError(c, err)
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Release(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReleaseDelete serves DELETE /repos/{owner}/{repo}/releases/{id}.
func handleReleaseDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "release_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err := d.Releases.DeleteRelease(c.Request().Context(), actor.UserID, owner, repo, id); err != nil {
			return mapReleaseError(c, err)
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleReleaseAssetsList serves GET /repos/{owner}/{repo}/releases/{id}/assets.
func handleReleaseAssetsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "release_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		assets, err := d.Releases.ListReleaseAssets(c.Request().Context(), actor.UserID, owner, repo, id)
		if err != nil {
			return mapReleaseError(c, err)
		}
		out := make([]any, len(assets))
		for i, a := range assets {
			out[i] = d.URLs.ReleaseAsset(owner, repo, id, a, d.NodeFormat)
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleReleaseAssetUpload serves POST /api/uploads/repos/{owner}/{repo}/releases/{id}/assets.
// The query parameters name (required) and label (optional) name the asset; the
// request body is the binary content of the asset. GitHub's documentation specifies
// Content-Type must be set by the client; it defaults to application/octet-stream.
func handleReleaseAssetUpload(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "release_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		name := c.Request().URL.Query().Get("name")
		if name == "" {
			writeError(c.Writer(), &apiError{Status: http.StatusUnprocessableEntity, Message: "name query parameter is required"})
			return nil
		}
		label := c.Request().URL.Query().Get("label")
		contentType := c.Request().Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		// Strip parameters from content type (e.g. "application/json; charset=utf-8")
		if mediaType, _, merr := mime.ParseMediaType(contentType); merr == nil {
			contentType = mediaType
		}
		r := c.Request().Body
		if r == nil {
			writeError(c.Writer(), &apiError{Status: http.StatusUnprocessableEntity, Message: "request body is required"})
			return nil
		}
		asset, err := d.Releases.UploadReleaseAsset(c.Request().Context(), actor.UserID, owner, repo, id, name, label, contentType, r)
		if err != nil {
			return mapReleaseError(c, err)
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.ReleaseAsset(owner, repo, id, asset, d.NodeFormat))
		return nil
	}
}

// handleReleaseAssetGet serves GET /repos/{owner}/{repo}/releases/assets/{id}.
func handleReleaseAssetGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "asset_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		// Check if the client wants the binary (Accept: application/octet-stream)
		// or the metadata (Accept: application/vnd.github+json).
		if wantsOctetStream(c.Request()) {
			path, serveErr := d.Releases.ServeAsset(c.Request().Context(), id)
			if serveErr != nil {
				return mapReleaseError(c, serveErr)
			}
			http.ServeFile(c.Writer(), c.Request(), path)
			return nil
		}
		a, err := d.Releases.GetReleaseAsset(c.Request().Context(), actor.UserID, owner, repo, id)
		if err != nil {
			return mapReleaseError(c, err)
		}
		// Placeholder release ID for URL building; the asset knows its own releasePK
		// but not the release's db_id. Build via its own ID only.
		writeJSON(c.Writer(), http.StatusOK, d.URLs.ReleaseAsset(owner, repo, 0, a, d.NodeFormat))
		return nil
	}
}

// handleReleaseAssetEdit serves PATCH /repos/{owner}/{repo}/releases/assets/{id}.
func handleReleaseAssetEdit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "asset_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body assetEditBody
		if !decodeJSON(c, &body) {
			return nil
		}
		a, err := d.Releases.UpdateReleaseAsset(c.Request().Context(), actor.UserID, owner, repo, id, body.Name, body.Label)
		if err != nil {
			return mapReleaseError(c, err)
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.ReleaseAsset(owner, repo, 0, a, d.NodeFormat))
		return nil
	}
}

// handleReleaseAssetDelete serves DELETE /repos/{owner}/{repo}/releases/assets/{id}.
func handleReleaseAssetDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner := c.Param("owner")
		repo := c.Param("repo")
		id, err := parseID(c, "asset_id")
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err := d.Releases.DeleteReleaseAsset(c.Request().Context(), actor.UserID, owner, repo, id); err != nil {
			return mapReleaseError(c, err)
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// parseID parses an integer path parameter by name.
func parseID(c *mizu.Ctx, paramName string) (int64, error) {
	return strconv.ParseInt(c.Param(paramName), 10, 64)
}

// wantsOctetStream checks if the client prefers binary content.
func wantsOctetStream(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == "application/octet-stream"
}

// mapReleaseError maps domain release errors to HTTP errors.
func mapReleaseError(c *mizu.Ctx, err error) error {
	switch {
	case errors.Is(err, domain.ErrReleaseNotFound), errors.Is(err, domain.ErrReleaseAssetNotFound):
		writeError(c.Writer(), errNotFound())
		return nil
	case errors.Is(err, domain.ErrRepoNotFound):
		writeError(c.Writer(), errNotFound())
		return nil
	case errors.Is(err, domain.ErrForbidden):
		writeError(c.Writer(), &apiError{Status: http.StatusForbidden, Message: "Must have write access to the repository"})
		return nil
	case errors.Is(err, domain.ErrValidation):
		writeError(c.Writer(), &apiError{Status: http.StatusUnprocessableEntity, Message: "Validation Failed"})
		return nil
	default:
		return err
	}
}
