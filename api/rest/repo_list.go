package rest

import (
	"sort"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
)

// repoListOpts are the parsed list-shaping parameters the repository list
// endpoints share: the type/visibility/affiliation selectors, the sort order,
// and the updated-at window. Which selectors an endpoint honors differs per
// route; parsing and validation live here so the 422 shapes stay uniform.
type repoListOpts struct {
	Type        string
	Visibility  string
	Affiliation map[string]bool
	Sort        string
	Direction   string
	Since       *time.Time
	Before      *time.Time
}

// parseRepoListOpts reads the shared repository list parameters. types is the
// set of values the endpoint's type parameter admits; an empty visibility or
// affiliation stays empty so the caller can tell "not sent" from a default.
// Sort defaults to full_name; direction defaults to ascending for full_name
// and descending for the time sorts, matching GitHub.
func parseRepoListOpts(c *mizu.Ctx, types ...string) (repoListOpts, *apiError) {
	opts := repoListOpts{}

	if v := c.Query("type"); v != "" {
		ok := false
		for _, t := range types {
			if v == t {
				ok = true
				break
			}
		}
		if !ok {
			return opts, errValidation(FieldError{Resource: "Repository", Field: "type", Code: "invalid"})
		}
		opts.Type = v
	}

	switch v := c.Query("visibility"); v {
	case "", "all", "public", "private":
		opts.Visibility = v
	default:
		return opts, errValidation(FieldError{Resource: "Repository", Field: "visibility", Code: "invalid"})
	}

	if v := c.Query("affiliation"); v != "" {
		opts.Affiliation = map[string]bool{}
		for _, a := range strings.Split(v, ",") {
			switch a = strings.TrimSpace(a); a {
			case "owner", "collaborator", "organization_member":
				opts.Affiliation[a] = true
			default:
				return opts, errValidation(FieldError{Resource: "Repository", Field: "affiliation", Code: "invalid"})
			}
		}
	}

	switch v := c.Query("sort"); v {
	case "", "full_name", "created", "updated", "pushed":
		opts.Sort = v
		if v == "" {
			opts.Sort = "full_name"
		}
	default:
		return opts, errValidation(FieldError{Resource: "Repository", Field: "sort", Code: "invalid"})
	}

	switch v := c.Query("direction"); v {
	case "asc", "desc":
		opts.Direction = v
	case "":
		if opts.Sort == "full_name" {
			opts.Direction = "asc"
		} else {
			opts.Direction = "desc"
		}
	default:
		return opts, errValidation(FieldError{Resource: "Repository", Field: "direction", Code: "invalid"})
	}

	var perr *apiError
	if opts.Since, perr = repoTimeQuery(c, "since"); perr != nil {
		return opts, perr
	}
	if opts.Before, perr = repoTimeQuery(c, "before"); perr != nil {
		return opts, perr
	}
	return opts, nil
}

// repoTimeQuery reads an ISO 8601 timestamp parameter for the repository
// lists, reporting the Repository-resource 422 on a malformed value.
func repoTimeQuery(c *mizu.Ctx, name string) (*time.Time, *apiError) {
	v := c.Query(name)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, errValidation(FieldError{Resource: "Repository", Field: name, Code: "invalid"})
	}
	return &t, nil
}

// filterRepoVisibility keeps the repositories the visibility selector admits.
func filterRepoVisibility(repos []*domain.Repo, visibility string) []*domain.Repo {
	if visibility == "" || visibility == "all" {
		return repos
	}
	wantPrivate := visibility == "private"
	out := repos[:0]
	for _, r := range repos {
		if r.Private == wantPrivate {
			out = append(out, r)
		}
	}
	return out
}

// filterRepoWindow keeps the repositories whose updated_at falls inside the
// since/before window, GitHub's semantics for the /user/repos time filters.
func filterRepoWindow(repos []*domain.Repo, since, before *time.Time) []*domain.Repo {
	if since == nil && before == nil {
		return repos
	}
	out := repos[:0]
	for _, r := range repos {
		if since != nil && !r.UpdatedAt.After(*since) {
			continue
		}
		if before != nil && !r.UpdatedAt.Before(*before) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// filterRepoFork keeps only forks (forks) or only non-forks (sources), the
// org-list type selectors.
func filterRepoFork(repos []*domain.Repo, wantFork bool) []*domain.Repo {
	out := repos[:0]
	for _, r := range repos {
		if r.Fork == wantFork {
			out = append(out, r)
		}
	}
	return out
}

// sortRepos orders the list by the parsed sort and direction. full_name
// compares case-insensitively on owner/name; created, updated, and pushed
// compare the matching timestamps, with a nil pushed_at sorting oldest.
func sortRepos(repos []*domain.Repo, by, direction string) {
	asc := direction != "desc"
	less := func(i, j int) bool {
		a, b := repos[i], repos[j]
		var before bool
		switch by {
		case "created":
			before = a.CreatedAt.Before(b.CreatedAt)
		case "updated":
			before = a.UpdatedAt.Before(b.UpdatedAt)
		case "pushed":
			at, bt := time.Time{}, time.Time{}
			if a.PushedAt != nil {
				at = *a.PushedAt
			}
			if b.PushedAt != nil {
				bt = *b.PushedAt
			}
			before = at.Before(bt)
		default:
			before = strings.ToLower(a.FullName()) < strings.ToLower(b.FullName())
		}
		if asc {
			return before
		}
		return !before
	}
	sort.SliceStable(repos, less)
}

// mergeRepos appends the repositories from more that base does not already
// hold, keyed by internal PK, preserving first-seen order before sorting.
func mergeRepos(base, more []*domain.Repo) []*domain.Repo {
	seen := make(map[int64]bool, len(base))
	for _, r := range base {
		seen[r.PK] = true
	}
	for _, r := range more {
		if !seen[r.PK] {
			seen[r.PK] = true
			base = append(base, r)
		}
	}
	return base
}
