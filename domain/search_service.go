package domain

import (
	"bytes"
	"context"
	"errors"
	"path"
	"strings"
	"sync"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/search"
	"github.com/tamnd/githome/store"
)

// SearchStore is the slice of the store the search service needs: the
// cross-repository issue, repository, and code scans, the code index the
// service maintains, the visible-repository lookup that scopes code search,
// and the login and owner/name resolution that turns qualifiers into the
// internal pks the scans filter on.
type SearchStore interface {
	SearchIssues(ctx context.Context, q store.IssueSearch) ([]store.IssueRow, error)
	CountSearchIssues(ctx context.Context, q store.IssueSearch) (int, error)
	SearchRepositories(ctx context.Context, q store.RepoSearch) ([]store.RepoRow, error)
	CountSearchRepositories(ctx context.Context, q store.RepoSearch) (int, error)
	SearchCode(ctx context.Context, q store.CodeSearch) ([]store.CodeHit, error)
	CountSearchCode(ctx context.Context, q store.CodeSearch) (int, error)
	SearchUsers(ctx context.Context, q store.UserSearch) ([]store.UserRow, error)
	CountSearchUsers(ctx context.Context, q store.UserSearch) (int, error)
	CodeIndexHead(ctx context.Context, repoPK int64) (string, error)
	CodeIndexTruncated(ctx context.Context, repoPKs []int64) (bool, error)
	ReplaceCodeDocs(ctx context.Context, repoPK int64, headSHA string, truncated bool, docs []store.CodeDoc) error
	VisibleRepoPKs(ctx context.Context, viewerPK int64, ownerPKs []int64) ([]int64, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
}

// SearchService runs the three search surfaces over the store and git. Issue,
// repository, and code search are filtered index scans; the service also owns
// the code index, rebuilding a repository's documents from its head tree when
// the push worker asks or when a search finds the index behind the head. It
// reuses the repo and issue services to assemble the domain values it returns,
// so visibility and rendering stay defined in one place. The store scans
// already gate visibility by viewer, so a private repository never appears in
// another viewer's results.
type SearchService struct {
	store    SearchStore
	repos    *RepoService
	issues   *IssueService
	gitStore *git.Store

	// idxLocks serializes index rebuilds per repository (values are *sync.Mutex
	// keyed by repo pk), so concurrent searches over a stale repo do not race
	// to walk the same tree.
	idxLocks sync.Map
}

// NewSearchService builds a SearchService over the store, the repo and issue
// services, and the git store code search reads blobs from.
func NewSearchService(st SearchStore, repos *RepoService, issues *IssueService, gs *git.Store) *SearchService {
	return &SearchService{store: st, repos: repos, issues: issues, gitStore: gs}
}

// codeIndexMaxFiles caps how many files a single repository contributes to the
// code index; a tree larger than this is indexed up to the cap and marked
// truncated, which search reports as incomplete_results.
const codeIndexMaxFiles = 20000

// codeIndexMaxFileBytes caps the content size a single file contributes to the
// index. Larger files (and binary ones) are indexed by path only, matching
// GitHub's 384 KB code search ceiling.
const codeIndexMaxFileBytes = 384 << 10

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

// SearchUsers runs an account search over the users table and returns the page
// of matched accounts plus the total match count. The free-text terms match
// the login, name, and public email; the type: qualifier narrows to user or
// org accounts. Unlike code search it spans every account, since accounts are
// public; visibility never enters into it.
func (s *SearchService) SearchUsers(ctx context.Context, raw, sort, order string, page, perPage int) ([]*User, int, error) {
	q := search.Parse(raw)
	f := store.UserSearch{
		Terms:  q.Terms,
		Sort:   userSearchSort(sort),
		Order:  search.NormalizeOrder(order),
		Limit:  perPage,
		Offset: offsetFor(page, perPage),
	}
	if v, ok := q.First("type"); ok {
		f.Type = v
	}
	rows, err := s.store.SearchUsers(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountSearchUsers(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*User, 0, len(rows))
	for i := range rows {
		out = append(out, userFromRow(&rows[i]))
	}
	return out, total, nil
}

// SearchCode queries the code index over the repositories the query scopes to
// and returns the files whose path or content matches the free-text terms. It
// requires a repo:, user:, or org: qualifier, matching GitHub's rule that a
// code search cannot span every repository on the host. Before querying it
// brings each scoped repository's index up to its current head, so a search
// right after a push (or after a web edit that never went through the push
// path) still sees the new content. The bool result reports whether any scoped
// index is truncated, which becomes the envelope's incomplete_results.
func (s *SearchService) SearchCode(ctx context.Context, viewerPK int64, raw string, page, perPage int) ([]CodeResult, int, bool, error) {
	q := search.Parse(raw)
	repoPKs, ok, err := s.codeScope(ctx, viewerPK, q)
	if err != nil {
		return nil, 0, false, err
	}
	if !ok {
		return nil, 0, false, ErrSearchScopeRequired
	}

	repoMap := make(map[int64]*Repo, len(repoPKs))
	for _, pk := range repoPKs {
		repo, err := s.repos.RepoForEvent(ctx, pk)
		if err != nil {
			return nil, 0, false, err
		}
		repoMap[pk] = repo
		if err := s.ensureCodeIndex(ctx, repo); err != nil {
			return nil, 0, false, err
		}
	}

	f := store.CodeSearch{
		RepoPKs: repoPKs,
		Terms:   q.Terms,
		Limit:   pageSize(perPage),
		Offset:  offsetFor(page, perPage),
	}
	hits, err := s.store.SearchCode(ctx, f)
	if err != nil {
		return nil, 0, false, err
	}
	total, err := s.store.CountSearchCode(ctx, f)
	if err != nil {
		return nil, 0, false, err
	}
	incomplete, err := s.store.CodeIndexTruncated(ctx, repoPKs)
	if err != nil {
		return nil, 0, false, err
	}

	out := make([]CodeResult, 0, len(hits))
	for _, h := range hits {
		repo, ok := repoMap[h.RepoPK]
		if !ok {
			continue
		}
		out = append(out, CodeResult{
			Repo: repo,
			Path: h.Path,
			Name: path.Base(h.Path),
			SHA:  h.SHA,
		})
	}
	return out, total, incomplete, nil
}

// ReindexRepoCode rebuilds the repository's code index when its head moved.
// The reindex_search worker calls it after a default-branch push; searches call
// the same path lazily through ensureCodeIndex, so the worker is an
// optimization (warm index before anyone searches), not a correctness
// requirement.
func (s *SearchService) ReindexRepoCode(ctx context.Context, repoPK int64) error {
	repo, err := s.repos.RepoForEvent(ctx, repoPK)
	if err != nil {
		return err
	}
	return s.ensureCodeIndex(ctx, repo)
}

// ensureCodeIndex brings the repository's code index up to its current default
// branch head, doing nothing when it already is. Rebuilds are serialized per
// repository and the head is re-checked under the lock, so concurrent searches
// over a stale repository produce one walk, not several.
func (s *SearchService) ensureCodeIndex(ctx context.Context, repo *Repo) error {
	head := s.codeIndexHeadSHA(repo)
	if fresh, err := s.codeIndexFresh(ctx, repo.PK, head); err != nil || fresh {
		return err
	}

	muAny, _ := s.idxLocks.LoadOrStore(repo.PK, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Another request may have rebuilt the index while we waited on the lock.
	if fresh, err := s.codeIndexFresh(ctx, repo.PK, head); err != nil || fresh {
		return err
	}
	docs, truncated := s.buildCodeDocs(repo)
	return s.store.ReplaceCodeDocs(ctx, repo.PK, head, truncated, docs)
}

// codeIndexHeadSHA resolves the commit the repository's index should be built
// from: the default branch head, or "" for an empty repository (indexed as an
// empty document set so the next search skips the walk).
func (s *SearchService) codeIndexHeadSHA(repo *Repo) string {
	br, err := s.repos.DefaultBranchRef(repo)
	if err != nil {
		return ""
	}
	return br.Commit
}

// codeIndexFresh reports whether the stored index state already matches head.
// A repository that was never indexed is not fresh.
func (s *SearchService) codeIndexFresh(ctx context.Context, repoPK int64, head string) (bool, error) {
	cur, err := s.store.CodeIndexHead(ctx, repoPK)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return cur == head, nil
}

// buildCodeDocs walks the repository's head tree into index documents. Every
// blob is indexed by path; content rides along only for text files under the
// size cap, so binary and oversized files still answer path queries. truncated
// reports that the tree exceeded the file ceiling and the index is partial.
func (s *SearchService) buildCodeDocs(repo *Repo) (docs []store.CodeDoc, truncated bool) {
	tree, err := s.repos.GetTree(repo, repo.DefaultBranch, true)
	if err != nil {
		// An empty or unreadable repository indexes as no documents.
		return nil, false
	}
	truncated = tree.Truncated
	for _, e := range tree.Entries {
		if e.Type != git.ObjectBlob {
			continue
		}
		if len(docs) >= codeIndexMaxFiles {
			truncated = true
			break
		}
		content := ""
		if e.Size <= codeIndexMaxFileBytes {
			if blob, err := s.repos.GetBlob(repo, e.SHA); err == nil && isText(blob.Content) && bytes.IndexByte(blob.Content, 0) < 0 {
				// ToValidUTF8 keeps a stray invalid byte from poisoning the row
				// (Postgres rejects non-UTF-8 text); valid content passes through.
				content = strings.ToValidUTF8(string(blob.Content), "�")
			}
		}
		docs = append(docs, store.CodeDoc{Path: e.Path, SHA: e.SHA, Content: content})
	}
	return docs, truncated
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

func userSearchSort(sort string) string {
	switch strings.ToLower(sort) {
	case "joined", "followers", "repositories":
		return strings.ToLower(sort)
	default:
		return ""
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
