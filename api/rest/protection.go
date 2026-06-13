package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/store"
)

// branchProtectionBody is the request body for PUT /branches/{branch}/protection.
// The four object fields stay raw so the handler can tell "missing" (nil, a
// 422 on GitHub) apart from an explicit null.
type branchProtectionBody struct {
	RequiredStatusChecks       json.RawMessage `json:"required_status_checks"`
	EnforceAdmins              json.RawMessage `json:"enforce_admins"`
	RequiredPullRequestReviews json.RawMessage `json:"required_pull_request_reviews"`
	Restrictions               json.RawMessage `json:"restrictions"`

	RequiredLinearHistory          bool `json:"required_linear_history"`
	AllowForcePushes               bool `json:"allow_force_pushes"`
	AllowDeletions                 bool `json:"allow_deletions"`
	BlockCreations                 bool `json:"block_creations"`
	RequiredConversationResolution bool `json:"required_conversation_resolution"`
	LockBranch                     bool `json:"lock_branch"`
	AllowForkSyncing               bool `json:"allow_fork_syncing"`
}

// protectionStatusChecks is the required_status_checks object of the PUT body
// and the required_status_checks PATCH body. checks is GitHub's replacement
// for the deprecated contexts; both are accepted and merged.
type protectionStatusChecks struct {
	Strict   *bool     `json:"strict"`
	Contexts *[]string `json:"contexts"`
	Checks   *[]struct {
		Context string `json:"context"`
		AppID   *int64 `json:"app_id"`
	} `json:"checks"`
}

// protectionReviews is the required_pull_request_reviews object of the PUT
// body and the required_pull_request_reviews PATCH body.
type protectionReviews struct {
	DismissStaleReviews          *bool `json:"dismiss_stale_reviews"`
	RequireCodeOwnerReviews      *bool `json:"require_code_owner_reviews"`
	RequiredApprovingReviewCount *int  `json:"required_approving_review_count"`
}

// protectionRestrictions is the restrictions object of the PUT body.
type protectionRestrictions struct {
	Users []string `json:"users"`
	Teams []string `json:"teams"`
	Apps  []string `json:"apps"`
}

// rawPresent reports whether a raw body field was supplied at all (an explicit
// null counts as present, matching GitHub's PUT contract).
func rawPresent(raw json.RawMessage) bool {
	return len(raw) > 0
}

// rawIsNull reports whether a supplied raw field is the JSON null literal.
func rawIsNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

// contextsOf merges the deprecated contexts list with checks[].context,
// preserving order and dropping duplicates.
func (b protectionStatusChecks) contextsOf() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ctx string) {
		if ctx != "" && !seen[ctx] {
			seen[ctx] = true
			out = append(out, ctx)
		}
	}
	if b.Contexts != nil {
		for _, ctx := range *b.Contexts {
			add(ctx)
		}
	}
	if b.Checks != nil {
		for _, ch := range *b.Checks {
			add(ch.Context)
		}
	}
	return out
}

// loadProtectedRepo resolves the repo and authorizes the actor for protection
// writes (repository admin, here the owner). A nil repo means the response was
// already written.
func loadProtectedRepo(d Deps, c *mizu.Ctx) (*domain.Repo, error) {
	actor := auth.ActorFrom(c.Request().Context())
	if !actor.IsUser() {
		writeError(c.Writer(), errRequiresAuth())
		return nil, nil
	}
	repo, err := loadRepo(d, c)
	if repo == nil {
		return nil, err
	}
	if actor.UserID != repo.OwnerPK {
		writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
		return nil, nil
	}
	return repo, nil
}

// loadProtection loads the protection rule for the branch in the URL, writing
// the 404 GitHub answers for an unprotected branch. A nil row means the
// response was already written.
func loadProtection(d Deps, c *mizu.Ctx, repo *domain.Repo) (*store.BranchProtectionRow, error) {
	p, err := d.Keys.GetBranchProtection(c.Request().Context(), repo.PK, c.Param("branch"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Branch not protected"})
		return nil, nil
	}
	return p, err
}

// handleBranchProtectionGet serves GET /repos/{owner}/{repo}/branches/{branch}/protection.
func handleBranchProtectionGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		p, err := d.Keys.GetBranchProtection(c.Request().Context(), repo.PK, c.Param("branch"))
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Branch not protected"})
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, branchProtectionToJSON(d, c, p))
		return nil
	}
}

// handleBranchProtectionPut serves PUT /repos/{owner}/{repo}/branches/{branch}/protection.
// GitHub demands all four top-level keys (required_status_checks,
// enforce_admins, required_pull_request_reviews, restrictions) even when null.
func handleBranchProtectionPut(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		repo, err := loadProtectedRepo(d, c)
		if repo == nil {
			return err
		}
		var body branchProtectionBody
		if !decodeJSON(c, &body) {
			return nil
		}
		for _, key := range []struct {
			name string
			raw  json.RawMessage
		}{
			{"required_status_checks", body.RequiredStatusChecks},
			{"enforce_admins", body.EnforceAdmins},
			{"required_pull_request_reviews", body.RequiredPullRequestReviews},
			{"restrictions", body.Restrictions},
		} {
			if !rawPresent(key.raw) {
				writeError(c.Writer(), errUnprocessable("Invalid request.\n\n\""+key.name+"\" wasn't supplied."))
				return nil
			}
		}
		p := &store.BranchProtectionRow{
			RepoPK:              repo.PK,
			BranchPattern:       c.Param("branch"),
			AllowForcePushes:    body.AllowForcePushes,
			AllowDeletions:      body.AllowDeletions,
			StatusCheckContexts: "[]",
			RestrictionsUsers:   "[]",
			RestrictionsTeams:   "[]",

			RequiredLinearHistory:          body.RequiredLinearHistory,
			BlockCreations:                 body.BlockCreations,
			RequiredConversationResolution: body.RequiredConversationResolution,
			LockBranch:                     body.LockBranch,
			AllowForkSyncing:               body.AllowForkSyncing,
		}
		// PUT replaces the whole rule, but required_signatures has no slot in
		// GitHub's PUT body: it survives from the previous rule.
		if prev, err := d.Keys.GetBranchProtection(ctx, repo.PK, p.BranchPattern); err == nil {
			p.RequiredSignatures = prev.RequiredSignatures
		}
		if !rawIsNull(body.EnforceAdmins) {
			if err := json.Unmarshal(body.EnforceAdmins, &p.EnforceAdmins); err != nil {
				writeError(c.Writer(), errUnprocessable("Invalid request.\n\n\"enforce_admins\" must be a boolean or null."))
				return nil
			}
		}
		if !rawIsNull(body.RequiredStatusChecks) {
			var checks protectionStatusChecks
			if err := json.Unmarshal(body.RequiredStatusChecks, &checks); err != nil {
				writeError(c.Writer(), errUnprocessable("Invalid request.\n\n\"required_status_checks\" must be an object or null."))
				return nil
			}
			p.RequireStatusChecks = true
			p.RequireBranchesUpToDate = checks.Strict != nil && *checks.Strict
			p.StatusCheckContexts = marshalStringList(checks.contextsOf())
		}
		if !rawIsNull(body.RequiredPullRequestReviews) {
			var reviews protectionReviews
			if err := json.Unmarshal(body.RequiredPullRequestReviews, &reviews); err != nil {
				writeError(c.Writer(), errUnprocessable("Invalid request.\n\n\"required_pull_request_reviews\" must be an object or null."))
				return nil
			}
			p.RequirePRReviews = true
			p.DismissStaleReviews = reviews.DismissStaleReviews != nil && *reviews.DismissStaleReviews
			p.RequireCodeOwnerReviews = reviews.RequireCodeOwnerReviews != nil && *reviews.RequireCodeOwnerReviews
			if reviews.RequiredApprovingReviewCount != nil {
				p.RequiredApprovingCount = *reviews.RequiredApprovingReviewCount
			}
		}
		if !rawIsNull(body.Restrictions) {
			var restr protectionRestrictions
			if err := json.Unmarshal(body.Restrictions, &restr); err != nil {
				writeError(c.Writer(), errUnprocessable("Invalid request.\n\n\"restrictions\" must be an object or null."))
				return nil
			}
			p.RestrictionsEnabled = true
			p.RestrictionsUsers = marshalStringList(restr.Users)
			p.RestrictionsTeams = marshalStringList(restr.Teams)
		}
		if err := d.Keys.SetBranchProtection(ctx, p); err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, branchProtectionToJSON(d, c, p))
		return nil
	}
}

// handleBranchProtectionDelete serves DELETE /repos/{owner}/{repo}/branches/{branch}/protection.
func handleBranchProtectionDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadProtectedRepo(d, c)
		if repo == nil {
			return err
		}
		if err := d.Keys.DeleteBranchProtection(c.Request().Context(), repo.PK, c.Param("branch")); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Branch not protected"})
				return nil
			}
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// protectionURL is the API URL of the branch's protection object.
func protectionURL(d Deps, c *mizu.Ctx) string {
	return d.URLs.API("repos", c.Param("owner"), c.Param("repo"), "branches", c.Param("branch"), "protection")
}

func branchProtectionToJSON(d Deps, c *mizu.Ctx, p *store.BranchProtectionRow) map[string]any {
	base := protectionURL(d, c)
	var reqStatusChecks any
	if p.RequireStatusChecks {
		reqStatusChecks = statusChecksToJSON(base, p)
	}
	var reqReviews any
	if p.RequirePRReviews {
		reqReviews = reviewsToJSON(base, p)
	}
	var restrictions any
	if p.RestrictionsEnabled {
		restrictions = restrictionsToJSON(d, c, base, p)
	}
	return map[string]any{
		"url":                              base,
		"required_status_checks":           reqStatusChecks,
		"enforce_admins":                   map[string]any{"url": base + "/enforce_admins", "enabled": p.EnforceAdmins},
		"required_pull_request_reviews":    reqReviews,
		"restrictions":                     restrictions,
		"required_signatures":              map[string]any{"url": base + "/required_signatures", "enabled": p.RequiredSignatures},
		"required_linear_history":          map[string]any{"enabled": p.RequiredLinearHistory},
		"allow_force_pushes":               map[string]any{"enabled": p.AllowForcePushes},
		"allow_deletions":                  map[string]any{"enabled": p.AllowDeletions},
		"block_creations":                  map[string]any{"enabled": p.BlockCreations},
		"required_conversation_resolution": map[string]any{"enabled": p.RequiredConversationResolution},
		"lock_branch":                      map[string]any{"enabled": p.LockBranch},
		"allow_fork_syncing":               map[string]any{"enabled": p.AllowForkSyncing},
	}
}

func statusChecksToJSON(base string, p *store.BranchProtectionRow) map[string]any {
	contexts := unmarshalStringList(p.StatusCheckContexts)
	if contexts == nil {
		contexts = []string{}
	}
	checks := make([]map[string]any, 0, len(contexts))
	for _, ctx := range contexts {
		checks = append(checks, map[string]any{"context": ctx, "app_id": nil})
	}
	return map[string]any{
		"url":          base + "/required_status_checks",
		"strict":       p.RequireBranchesUpToDate,
		"contexts":     contexts,
		"contexts_url": base + "/required_status_checks/contexts",
		"checks":       checks,
	}
}

func reviewsToJSON(base string, p *store.BranchProtectionRow) map[string]any {
	return map[string]any{
		"url":                             base + "/required_pull_request_reviews",
		"dismiss_stale_reviews":           p.DismissStaleReviews,
		"require_code_owner_reviews":      p.RequireCodeOwnerReviews,
		"required_approving_review_count": p.RequiredApprovingCount,
	}
}

func restrictionsToJSON(d Deps, c *mizu.Ctx, base string, p *store.BranchProtectionRow) map[string]any {
	return map[string]any{
		"url":       base + "/restrictions",
		"users_url": base + "/restrictions/users",
		"teams_url": base + "/restrictions/teams",
		"apps_url":  base + "/restrictions/apps",
		"users":     restrictionUsersToJSON(d, c, unmarshalStringList(p.RestrictionsUsers)),
		"teams":     restrictionTeamsToJSON(unmarshalStringList(p.RestrictionsTeams)),
		"apps":      []any{},
	}
}

// restrictionUsersToJSON renders restriction logins as user objects, the shape
// GitHub uses. Logins that no longer resolve still come back as a bare login so
// the round-trip never silently drops an entry.
func restrictionUsersToJSON(d Deps, c *mizu.Ctx, logins []string) []any {
	out := make([]any, 0, len(logins))
	for _, login := range logins {
		if d.Users != nil && d.URLs != nil {
			if u, err := d.Users.ByLogin(c.Request().Context(), login); err == nil {
				out = append(out, d.URLs.SimpleUser(u, d.NodeFormat))
				continue
			}
		}
		out = append(out, map[string]any{"login": login, "type": "User"})
	}
	return out
}

// restrictionTeamsToJSON renders restriction team slugs as minimal team objects.
func restrictionTeamsToJSON(slugs []string) []any {
	out := make([]any, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, map[string]any{"name": slug, "slug": slug})
	}
	return out
}

// handleProtectionToggle serves the GET/POST/DELETE triple of the two boolean
// sub-endpoints, enforce_admins and required_signatures: GET reads the flag,
// POST enables it, DELETE disables it.
func handleProtectionToggle(d Deps, slug string, get func(*store.BranchProtectionRow) bool, set func(*store.BranchProtectionRow, bool)) mizu.Handler {
	return func(c *mizu.Ctx) error {
		write := c.Request().Method != http.MethodGet
		var repo *domain.Repo
		var err error
		if write {
			repo, err = loadProtectedRepo(d, c)
		} else {
			repo, err = loadRepo(d, c)
		}
		if repo == nil {
			return err
		}
		p, err := loadProtection(d, c, repo)
		if p == nil {
			return err
		}
		if write {
			set(p, c.Request().Method == http.MethodPost)
			if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
				return err
			}
			if c.Request().Method == http.MethodDelete {
				c.Writer().WriteHeader(http.StatusNoContent)
				return nil
			}
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"url":     protectionURL(d, c) + "/" + slug,
			"enabled": get(p),
		})
		return nil
	}
}

// handleRequiredStatusChecks serves GET/PATCH/DELETE
// /branches/{branch}/protection/required_status_checks.
func handleRequiredStatusChecks(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		write := c.Request().Method != http.MethodGet
		var repo *domain.Repo
		var err error
		if write {
			repo, err = loadProtectedRepo(d, c)
		} else {
			repo, err = loadRepo(d, c)
		}
		if repo == nil {
			return err
		}
		p, err := loadProtection(d, c, repo)
		if p == nil {
			return err
		}
		if !p.RequireStatusChecks {
			writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Required status checks not enabled"})
			return nil
		}
		switch c.Request().Method {
		case http.MethodPatch:
			var body protectionStatusChecks
			if !decodeJSON(c, &body) {
				return nil
			}
			if body.Strict != nil {
				p.RequireBranchesUpToDate = *body.Strict
			}
			if body.Contexts != nil || body.Checks != nil {
				p.StatusCheckContexts = marshalStringList(body.contextsOf())
			}
			if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
				return err
			}
		case http.MethodDelete:
			p.RequireStatusChecks = false
			p.RequireBranchesUpToDate = false
			p.StatusCheckContexts = "[]"
			if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
				return err
			}
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		}
		writeJSON(c.Writer(), http.StatusOK, statusChecksToJSON(protectionURL(d, c), p))
		return nil
	}
}

// contextsBody is the body of the required_status_checks/contexts write
// endpoints: GitHub documents {"contexts": [...]} and historically accepted a
// raw array; both are read.
func contextsBody(c *mizu.Ctx) ([]string, bool) {
	var raw json.RawMessage
	if !decodeJSON(c, &raw) {
		return nil, false
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, true
	}
	var wrapped struct {
		Contexts []string `json:"contexts"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return wrapped.Contexts, true
	}
	writeError(c.Writer(), errUnprocessable("Validation Failed"))
	return nil, false
}

// handleStatusCheckContexts serves GET/POST/PUT/DELETE
// /branches/{branch}/protection/required_status_checks/contexts. POST appends,
// PUT replaces, DELETE removes the named contexts; every success answers the
// resulting full list.
func handleStatusCheckContexts(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		write := c.Request().Method != http.MethodGet
		var repo *domain.Repo
		var err error
		if write {
			repo, err = loadProtectedRepo(d, c)
		} else {
			repo, err = loadRepo(d, c)
		}
		if repo == nil {
			return err
		}
		p, err := loadProtection(d, c, repo)
		if p == nil {
			return err
		}
		if !p.RequireStatusChecks {
			writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Required status checks not enabled"})
			return nil
		}
		contexts := unmarshalStringList(p.StatusCheckContexts)
		if write {
			body, ok := contextsBody(c)
			if !ok {
				return nil
			}
			switch c.Request().Method {
			case http.MethodPost:
				for _, ctx := range body {
					found := false
					for _, have := range contexts {
						if have == ctx {
							found = true
							break
						}
					}
					if !found {
						contexts = append(contexts, ctx)
					}
				}
			case http.MethodPut:
				contexts = body
			case http.MethodDelete:
				kept := contexts[:0]
				for _, have := range contexts {
					drop := false
					for _, ctx := range body {
						if have == ctx {
							drop = true
							break
						}
					}
					if !drop {
						kept = append(kept, have)
					}
				}
				contexts = kept
			}
			p.StatusCheckContexts = marshalStringList(contexts)
			if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
				return err
			}
		}
		if contexts == nil {
			contexts = []string{}
		}
		writeJSON(c.Writer(), http.StatusOK, contexts)
		return nil
	}
}

// handleRequiredPullRequestReviews serves GET/PATCH/DELETE
// /branches/{branch}/protection/required_pull_request_reviews.
func handleRequiredPullRequestReviews(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		write := c.Request().Method != http.MethodGet
		var repo *domain.Repo
		var err error
		if write {
			repo, err = loadProtectedRepo(d, c)
		} else {
			repo, err = loadRepo(d, c)
		}
		if repo == nil {
			return err
		}
		p, err := loadProtection(d, c, repo)
		if p == nil {
			return err
		}
		if !p.RequirePRReviews {
			writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Required pull request reviews not enabled"})
			return nil
		}
		switch c.Request().Method {
		case http.MethodPatch:
			var body protectionReviews
			if !decodeJSON(c, &body) {
				return nil
			}
			if body.DismissStaleReviews != nil {
				p.DismissStaleReviews = *body.DismissStaleReviews
			}
			if body.RequireCodeOwnerReviews != nil {
				p.RequireCodeOwnerReviews = *body.RequireCodeOwnerReviews
			}
			if body.RequiredApprovingReviewCount != nil {
				p.RequiredApprovingCount = *body.RequiredApprovingReviewCount
			}
			if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
				return err
			}
		case http.MethodDelete:
			p.RequirePRReviews = false
			p.DismissStaleReviews = false
			p.RequireCodeOwnerReviews = false
			p.RequiredApprovingCount = 0
			if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
				return err
			}
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		}
		writeJSON(c.Writer(), http.StatusOK, reviewsToJSON(protectionURL(d, c), p))
		return nil
	}
}

// handleRestrictions serves GET/DELETE /branches/{branch}/protection/restrictions.
func handleRestrictions(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		write := c.Request().Method != http.MethodGet
		var repo *domain.Repo
		var err error
		if write {
			repo, err = loadProtectedRepo(d, c)
		} else {
			repo, err = loadRepo(d, c)
		}
		if repo == nil {
			return err
		}
		p, err := loadProtection(d, c, repo)
		if p == nil {
			return err
		}
		if !p.RestrictionsEnabled {
			writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Push restrictions not enabled"})
			return nil
		}
		if c.Request().Method == http.MethodDelete {
			p.RestrictionsEnabled = false
			p.RestrictionsUsers = "[]"
			p.RestrictionsTeams = "[]"
			if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
				return err
			}
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		}
		writeJSON(c.Writer(), http.StatusOK, restrictionsToJSON(d, c, protectionURL(d, c), p))
		return nil
	}
}

// restrictionListBody reads the users/teams restriction write bodies, which
// GitHub documents as {"users": [...]}/{"teams": [...]} and historically
// accepted as raw arrays.
func restrictionListBody(c *mizu.Ctx, key string) ([]string, bool) {
	var raw json.RawMessage
	if !decodeJSON(c, &raw) {
		return nil, false
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, true
	}
	var wrapped map[string][]string
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return wrapped[key], true
	}
	writeError(c.Writer(), errUnprocessable("Validation Failed"))
	return nil, false
}

// handleRestrictionList serves GET/POST/PUT/DELETE on
// /branches/{branch}/protection/restrictions/{users,teams,apps}. POST appends,
// PUT replaces, DELETE removes; every success answers the resulting list in
// its object shape. Apps are accepted but always empty: Githome has no
// installable apps to restrict to.
func handleRestrictionList(d Deps, key string, get func(*store.BranchProtectionRow) string, set func(*store.BranchProtectionRow, string)) mizu.Handler {
	return func(c *mizu.Ctx) error {
		write := c.Request().Method != http.MethodGet
		var repo *domain.Repo
		var err error
		if write {
			repo, err = loadProtectedRepo(d, c)
		} else {
			repo, err = loadRepo(d, c)
		}
		if repo == nil {
			return err
		}
		p, err := loadProtection(d, c, repo)
		if p == nil {
			return err
		}
		if !p.RestrictionsEnabled {
			writeError(c.Writer(), &apiError{Status: http.StatusNotFound, Message: "Push restrictions not enabled"})
			return nil
		}
		var items []string
		if get != nil {
			items = unmarshalStringList(get(p))
		}
		if write {
			body, ok := restrictionListBody(c, key)
			if !ok {
				return nil
			}
			switch c.Request().Method {
			case http.MethodPost:
				for _, it := range body {
					found := false
					for _, have := range items {
						if have == it {
							found = true
							break
						}
					}
					if !found {
						items = append(items, it)
					}
				}
			case http.MethodPut:
				items = body
			case http.MethodDelete:
				kept := items[:0]
				for _, have := range items {
					drop := false
					for _, it := range body {
						if have == it {
							drop = true
							break
						}
					}
					if !drop {
						kept = append(kept, have)
					}
				}
				items = kept
			}
			if set != nil {
				set(p, marshalStringList(items))
				if err := d.Keys.SetBranchProtection(c.Request().Context(), p); err != nil {
					return err
				}
			}
		}
		switch key {
		case "users":
			writeJSON(c.Writer(), http.StatusOK, restrictionUsersToJSON(d, c, items))
		case "teams":
			writeJSON(c.Writer(), http.StatusOK, restrictionTeamsToJSON(items))
		default:
			writeJSON(c.Writer(), http.StatusOK, []any{})
		}
		return nil
	}
}

// marshalStringList renders a string slice as a JSON array, with nil treated as
// the empty list so the stored column is always a valid array.
func marshalStringList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalStringList parses a stored JSON array column, tolerating the empty
// string a fresh row may carry.
func unmarshalStringList(raw string) []string {
	var out []string
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
