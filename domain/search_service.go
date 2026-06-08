package domain

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/search"
	"github.com/tamnd/githome/store"
)

// SearchStore is the slice of the store the search service needs: the
// cross-repository issue and repository scans, the visible-repository lookup
// code search walks, and the login and owner/name resolution that turns
// qualifiers into the internal pks the scans filter on.
type SearchStore interface {
	SearchIssues(ctx context.Context, q store.IssueSearch) ([]store.IssueRow, error)
	CountSearchIssues(ctx context.Context, q store.IssueSearch) (int, error)
	SearchRepositories(ctx context.Context, q store.RepoSearch) ([]store.RepoRow, error)
	CountSearchRepositories(ctx context.Context, q store.RepoSearch) (int, error)
	VisibleRepoPKs(ctx context.Context, viewerPK int64, ownerPKs []int64) ([]int64, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
}

// SearchService runs the three search surfaces over the store and git. Issue and
// repository search are filtered scans; code search walks the head tree of each
// in-scope repository. It reuses the repo and issue services to assemble the
// domain values it returns, so visibility and rendering stay defined in one
// place. The store scans already gate visibility by viewer, so a private
// repository never appears in another viewer's results.
type SearchService struct {
	store    SearchStore
	repos    *RepoService
	issues   *IssueService
	gitStore *git.Store
}

// NewSearchService builds a SearchService over the store, the repo and issue
// services, and the git store code search reads blobs from.
func NewSearchService(st SearchStore, repos *RepoService, issues *IssueService, gs *git.Store) *SearchService {
	return &SearchService{store: st, repos: repos, issues: issues, gitStore: gs}
}

// codeScanLimit caps how many blobs a single code search reads before it stops
// and reports incomplete results, the way GitHub returns incomplete_results
// when a search does not finish. It bounds the cost of an unindexed walk.
const codeScanLimit = 2000

// impossiblePK is a primary key no row holds; a qualifier that resolves to
// nothing adds it so the scan returns an empty page rather than ignoring the
// filter (which would wrongly widen the results).
const impossiblePK int64 = -1

// SearchIssues runs an issue/pull-request search for the viewer and returns the
// page of assembled issues plus the total match count for the envelope.
func (s *SearchService) SearchIssues(ctx context.Context, viewerPK int64, raw, sort, order string, page, perPage int) ([]IssueHit, int, error) {
	q := search.Parse(raw)
	f := store.IssueSearch{
		ViewerPK: viewerPK,
		Terms:    q.Terms,
		Sort:     issueSearchSort(sort),
		Order:    search.NormalizeOrder(order),
		Limit:    perPage,
		Offset:   offsetFor(page, perPage),
	}
	f.MatchTitle, f.MatchBody = termFields(q)
	f.State = issueState(q)
	f.IsPull = issueType(q)

	var ok bool
	if f.AuthorPK, ok = s.resolveLogin(ctx, q, "author"); !ok {
		return []IssueHit{}, 0, nil
	}
	if f.AssigneePK, ok = s.resolveLogin(ctx, q, "assignee"); !ok {
		return []IssueHit{}, 0, nil
	}
	if f.RepoPKs, ok = s.resolveRepos(ctx, q); !ok {
		return []IssueHit{}, 0, nil
	}
	if f.OwnerPKs, ok = s.resolveOwners(ctx, q); !ok {
		return []IssueHit{}, 0, nil
	}
	f.Labels = q.Values("label")

	rows, err := s.store.SearchIssues(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountSearchIssues(ctx, f)
	if err != nil {
		return nil, 0, err
	}

	// Deduplicate repo PKs and load each repo once, then batch-assemble issues.
	repoMap := make(map[int64]*Repo, 4)
	for i := range rows {
		pk := rows[i].RepoPK
		if _, ok := repoMap[pk]; !ok {
			repo, err := s.repos.RepoForEvent(ctx, pk)
			if err != nil {
				return nil, 0, err
			}
			repoMap[pk] = repo
		}
	}
	issues, err := s.issues.assembleIssueSearch(ctx, repoMap, rows)
	if err != nil {
		return nil, 0, err
	}
	out := make([]IssueHit, 0, len(issues))
	for _, iss := range issues {
		if repo, ok := repoMap[iss.RepoPK]; ok {
			out = append(out, IssueHit{Issue: iss, Repo: repo})
		}
	}
	return out, total, nil
}

// SearchRepositories runs a repository search for the viewer and returns the
// page of assembled repositories plus the total match count.
func (s *SearchService) SearchRepositories(ctx context.Context, viewerPK int64, raw, sort, order string, page, perPage int) ([]*Repo, int, error) {
	q := search.Parse(raw)
	f := store.RepoSearch{
		ViewerPK: viewerPK,
		Terms:    q.Terms,
		Sort:     repoSearchSort(sort),
		Order:    search.NormalizeOrder(order),
		Fork:     forkFilter(q),
		Limit:    perPage,
		Offset:   offsetFor(page, perPage),
	}
	var ok bool
	if f.OwnerPKs, ok = s.resolveOwners(ctx, q); !ok {
		return []*Repo{}, 0, nil
	}

	rows, err := s.store.SearchRepositories(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountSearchRepositories(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*Repo, 0, len(rows))
	for i := range rows {
		repo, err := s.repos.RepoForEvent(ctx, rows[i].PK)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, repo)
	}
	return out, total, nil
}

// SearchCode walks the head tree of each repository the query scopes to and
// returns the files whose path or content matches the free-text terms. It
// requires a repo:, user:, or org: qualifier, since an unindexed walk cannot
// span every repository. The bool result reports whether the walk stopped at
// the scan limit before finishing, which becomes the envelope's
// incomplete_results.
func (s *SearchService) SearchCode(ctx context.Context, viewerPK int64, raw string, page, perPage int) ([]CodeResult, int, bool, error) {
	q := search.Parse(raw)
	repoPKs, ok, err := s.codeScope(ctx, viewerPK, q)
	if err != nil {
		return nil, 0, false, err
	}
	if !ok {
		return nil, 0, false, ErrSearchScopeRequired
	}

	terms := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		terms = append(terms, strings.ToLower(t))
	}

	var all []CodeResult
	scanned := 0
	incomplete := false
	for _, pk := range repoPKs {
		repo, err := s.repos.RepoForEvent(ctx, pk)
		if err != nil {
			return nil, 0, false, err
		}
		hits, used, capped := s.scanRepoCode(repo, terms, codeScanLimit-scanned)
		scanned += used
		all = append(all, hits...)
		if capped {
			incomplete = true
			break
		}
	}

	total := len(all)
	lo := offsetFor(page, perPage)
	if lo > total {
		lo = total
	}
	hi := lo + pageSize(perPage)
	if hi > total {
		hi = total
	}
	return all[lo:hi], total, incomplete, nil
}

// scanRepoCode reads the repository's head tree and returns the files matching
// every term. budget caps how many blobs it reads; used is how many it read,
// and capped reports that it ran out of budget before finishing.
func (s *SearchService) scanRepoCode(repo *Repo, terms []string, budget int) (hits []CodeResult, used int, capped bool) {
	tree, err := s.repos.GetTree(repo, repo.DefaultBranch, true)
	if err != nil {
		// An empty or unreadable repository contributes no code matches.
		return nil, 0, false
	}
	for _, e := range tree.Entries {
		if e.Type != git.ObjectBlob {
			continue
		}
		if used >= budget {
			return hits, used, true
		}
		used++
		if !s.matchBlob(repo, e, terms) {
			continue
		}
		hits = append(hits, CodeResult{
			Repo: repo,
			Path: e.Path,
			Name: path.Base(e.Path),
			SHA:  e.SHA,
		})
	}
	return hits, used, false
}

// matchBlob reports whether the blob at e satisfies every term. A term matches
// when it appears in the file path or, for a readable text blob, in the
// content. No terms means every file matches, the way an empty code query
// lists the scoped tree.
func (s *SearchService) matchBlob(repo *Repo, e git.TreeEntry, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	lowerPath := strings.ToLower(e.Path)
	var content string
	loaded := false
	for _, t := range terms {
		if strings.Contains(lowerPath, t) {
			continue
		}
		if !loaded {
			blob, err := s.repos.GetBlob(repo, e.SHA)
			if err == nil && isText(blob.Content) {
				content = strings.ToLower(string(blob.Content))
			}
			loaded = true
		}
		if !strings.Contains(content, t) {
			return false
		}
	}
	return true
}

// codeScope resolves the repository pks a code search runs over from its repo:,
// user:, and org: qualifiers. ok is false when the query names no scope, which
// the caller turns into ErrSearchScopeRequired.
func (s *SearchService) codeScope(ctx context.Context, viewerPK int64, q search.Query) (pks []int64, ok bool, err error) {
	hasScope := false
	seen := map[int64]bool{}
	for _, v := range q.Values("repo") {
		hasScope = true
		owner, name, found := strings.Cut(v, "/")
		if !found {
			continue
		}
		row, err := s.store.RepoByOwnerName(ctx, owner, name)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		// Re-check visibility through the repo service so a private repository
		// the viewer cannot see is silently dropped, never walked.
		if _, err := s.repos.GetRepoByID(ctx, viewerPK, row.DBID); err != nil {
			if errors.Is(err, ErrRepoNotFound) {
				continue
			}
			return nil, false, err
		}
		if !seen[row.PK] {
			seen[row.PK] = true
			pks = append(pks, row.PK)
		}
	}

	var owners []int64
	for _, kind := range []string{"user", "org"} {
		for _, login := range q.Values(kind) {
			hasScope = true
			u, err := s.store.UserByLogin(ctx, login)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, false, err
			}
			owners = append(owners, u.PK)
		}
	}
	if len(owners) > 0 {
		visible, err := s.store.VisibleRepoPKs(ctx, viewerPK, owners)
		if err != nil {
			return nil, false, err
		}
		for _, pk := range visible {
			if !seen[pk] {
				seen[pk] = true
				pks = append(pks, pk)
			}
		}
	}
	return pks, hasScope, nil
}

// resolveLogin resolves a single-login qualifier (author, assignee) to its
// internal pk. ok is false when the qualifier was given but no account
// matched, so the caller returns an empty page; a missing qualifier returns
// (nil, true).
func (s *SearchService) resolveLogin(ctx context.Context, q search.Query, key string) (*int64, bool) {
	login, present := q.First(key)
	if !present {
		return nil, true
	}
	u, err := s.store.UserByLogin(ctx, login)
	if err != nil {
		return nil, false
	}
	return &u.PK, true
}

// resolveOwners resolves user: and org: qualifiers to owner pks. ok is false
// when owner qualifiers were given but none resolved.
func (s *SearchService) resolveOwners(ctx context.Context, q search.Query) ([]int64, bool) {
	var (
		owners  []int64
		present bool
	)
	for _, kind := range []string{"user", "org"} {
		for _, login := range q.Values(kind) {
			present = true
			u, err := s.store.UserByLogin(ctx, login)
			if err != nil {
				continue
			}
			owners = append(owners, u.PK)
		}
	}
	if present && len(owners) == 0 {
		return []int64{impossiblePK}, false
	}
	return owners, true
}

// resolveRepos resolves repo: qualifiers to repository pks. ok is false when
// repo: qualifiers were given but none resolved.
func (s *SearchService) resolveRepos(ctx context.Context, q search.Query) ([]int64, bool) {
	var (
		pks     []int64
		present bool
	)
	for _, v := range q.Values("repo") {
		present = true
		owner, name, found := strings.Cut(v, "/")
		if !found {
			continue
		}
		row, err := s.store.RepoByOwnerName(ctx, owner, name)
		if err != nil {
			continue
		}
		pks = append(pks, row.PK)
	}
	if present && len(pks) == 0 {
		return []int64{impossiblePK}, false
	}
	return pks, true
}

// termFields reads the in: qualifiers into the title/body match flags, matching
// both when none are given. A comments-only request degrades to title and body,
// since the scan does not index comment text.
func termFields(q search.Query) (title, body bool) {
	for _, f := range search.Fields(q, search.FieldTitle, search.FieldBody) {
		switch f {
		case search.FieldTitle:
			title = true
		case search.FieldBody:
			body = true
		}
	}
	if !title && !body {
		return true, true
	}
	return title, body
}

// issueState reads a state from the state: qualifier or the is:open / is:closed
// forms, returning "" (no state filter) when neither is present.
func issueState(q search.Query) string {
	if v, ok := q.First("state"); ok {
		switch strings.ToLower(v) {
		case "open", "closed":
			return strings.ToLower(v)
		}
	}
	for _, v := range q.Values("is") {
		switch strings.ToLower(v) {
		case "open", "closed":
			return strings.ToLower(v)
		}
	}
	return ""
}

// issueType reads the type:/is: qualifier into the is_pull filter: issue-only,
// pull-request-only, or nil for both.
func issueType(q search.Query) *bool {
	want := func(v string) *bool {
		switch strings.ToLower(v) {
		case "issue":
			f := false
			return &f
		case "pr", "pull-request", "merged":
			f := true
			return &f
		}
		return nil
	}
	if v, ok := q.First("type"); ok {
		if p := want(v); p != nil {
			return p
		}
	}
	for _, v := range q.Values("is") {
		if p := want(v); p != nil {
			return p
		}
	}
	return nil
}

// forkFilter reads the fork: qualifier: fork:only keeps only forks, fork:false
// excludes them, and an absent qualifier leaves forks in.
func forkFilter(q search.Query) *bool {
	v, ok := q.First("fork")
	if !ok {
		return nil
	}
	switch strings.ToLower(v) {
	case "only", "true":
		t := true
		return &t
	case "false":
		f := false
		return &f
	}
	return nil
}

func issueSearchSort(sort string) string {
	switch strings.ToLower(sort) {
	case "updated", "comments":
		return strings.ToLower(sort)
	default:
		return "created"
	}
}

func repoSearchSort(sort string) string {
	switch strings.ToLower(sort) {
	case "updated":
		return "updated"
	default:
		return "created"
	}
}

func pageSize(perPage int) int {
	if perPage <= 0 {
		return 30
	}
	return perPage
}

// isText reports whether content looks like text rather than a binary blob, by
// the same heuristic git uses: a NUL byte in the head marks it binary. Code
// search only greps text.
func isText(content []byte) bool {
	head := content
	if len(head) > 8000 {
		head = head[:8000]
	}
	for _, b := range head {
		if b == 0 {
			return false
		}
	}
	return true
}
