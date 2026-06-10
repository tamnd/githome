package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// createRefBody is the POST /git/refs request: a fully qualified ref and the sha
// it should point at. GitHub requires ref to start with refs/.
type createRefBody struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// updateRefBody is the PATCH /git/refs/{ref} request: the new sha and whether to
// allow a non-fast-forward move. force defaults to false, matching GitHub.
type updateRefBody struct {
	SHA   string `json:"sha"`
	Force bool   `json:"force"`
}

// handleCreateRef serves POST /repos/{owner}/{repo}/git/refs. It creates the ref
// at sha and returns 201 with the ref object. A caller who can see but not write
// the repository gets 403; an invalid ref name, a ref that already exists, or a
// missing target object is 422.
func handleCreateRef(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body createRefBody
		if !decodeJSON(c, &body) {
			return nil
		}
		var missing []FieldError
		if body.Ref == "" {
			missing = append(missing, FieldError{Resource: "Reference", Field: "ref", Code: "missing_field"})
		}
		if body.SHA == "" {
			missing = append(missing, FieldError{Resource: "Reference", Field: "sha", Code: "missing_field"})
		}
		if len(missing) > 0 {
			writeError(c.Writer(), errValidation(missing...))
			return nil
		}

		actor := auth.ActorFrom(c.Request().Context())
		ref, err := d.Repos.CreateRef(c.Request().Context(), actor.UserID, repo.Owner.Login, repo.Name, body.Ref, body.SHA)
		if e := refWriteError(err); e != nil {
			writeError(c.Writer(), e)
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.GitRef(repo.Owner.Login, repo.Name, repo.ID, ref))
		return nil
	}
}

// handleUpdateRef serves PATCH /repos/{owner}/{repo}/git/refs/{ref}. The path ref
// omits the refs/ prefix (heads/featureA), matching GitHub; it is requalified
// before the write. A non-fast-forward move without force is 422.
func handleUpdateRef(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body updateRefBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.SHA == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Reference", Field: "sha", Code: "missing_field"}))
			return nil
		}

		ref := "refs/" + strings.TrimPrefix(c.Param("ref"), "refs/")
		actor := auth.ActorFrom(c.Request().Context())
		updated, err := d.Repos.UpdateRef(c.Request().Context(), actor.UserID, repo.Owner.Login, repo.Name, ref, body.SHA, body.Force)
		if e := refWriteError(err); e != nil {
			writeError(c.Writer(), e)
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.GitRef(repo.Owner.Login, repo.Name, repo.ID, updated))
		return nil
	}
}

// handleDeleteRef serves DELETE /repos/{owner}/{repo}/git/refs/{ref}. The path
// ref omits the refs/ prefix (heads/branch, tags/v1.0), matching GitHub; it is
// re-qualified before the delete. A successful delete returns 204.
func handleDeleteRef(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ref := "refs/" + strings.TrimPrefix(c.Param("ref"), "refs/")
		actor := auth.ActorFrom(c.Request().Context())
		if err := d.Repos.DeleteRef(c.Request().Context(), actor.UserID, repo.Owner.Login, repo.Name, ref); err != nil {
			if e := refWriteError(err); e != nil {
				writeError(c.Writer(), e)
				return nil
			}
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// refWriteError maps a ref-write domain error to its API response, or nil when
// err is nil or not a recognized ref-write error (the caller then falls through
// to the central 500 handler).
func refWriteError(err error) *apiError {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrForbidden):
		return errForbidden("Write access to the repository is required.")
	case errors.Is(err, domain.ErrInvalidRef):
		return errUnprocessable("The ref must be formatted as refs/heads/<branch> or refs/tags/<tag>.")
	case errors.Is(err, domain.ErrRefExists):
		return errUnprocessable("Reference already exists")
	case errors.Is(err, domain.ErrRefNotFound):
		return errUnprocessable("Reference does not exist")
	case errors.Is(err, domain.ErrObjectMissing):
		return errUnprocessable("Object does not exist")
	case errors.Is(err, domain.ErrNotFastForward):
		return errUnprocessable("Update is not a fast forward")
	default:
		return nil
	}
}

// decodeJSON reads the request body into v, writing a 400 and returning false on
// a malformed body. An empty body decodes to the zero value, which the field
// validation then rejects with a 422.
func decodeJSON(c *mizu.Ctx, v any) bool {
	dec := json.NewDecoder(c.Request().Body)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(c.Writer(), &apiError{
				Status:  http.StatusRequestEntityTooLarge,
				Message: "Request body is too large",
				DocURL:  docRoot,
			})
			return false
		}
		writeError(c.Writer(), &apiError{Status: http.StatusBadRequest, Message: "Problems parsing JSON", DocURL: docRoot})
		return false
	}
	return true
}
