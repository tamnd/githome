package presenter

import (
	"strconv"
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// GQLPullRequest renders a domain pull request into the GraphQL PullRequest shape
// for owner/repo. It fills the merge view from the worker-resolved state: a nil
// Mergeable is UNKNOWN, the null-then-value contract a poll resolves. The files
// and commits connections are paged through the git layer by their own
// resolvers; the presenter fills only the repository coordinates they read.
func (b *URLBuilder) GQLPullRequest(owner, repo string, pr *domain.PullRequest, format nodeid.Format) *gqlmodel.PullRequest {
	num := strconv.FormatInt(pr.Number, 10)
	fullID := gqlmodel.BigInt(strconv.FormatInt(pr.ID, 10))
	out := &gqlmodel.PullRequest{
		ID:                  nodeid.Encode(nodeid.KindPullRequest, pr.ID, format),
		Number:              int32(pr.Number),
		Title:               pr.Title,
		Body:                deref(pr.Body),
		State:               pullState(pr),
		URL:                 gqlmodel.URI(b.RepoHTML(owner, repo) + "/pull/" + num),
		Locked:              pr.Locked,
		Closed:              pr.State == "closed",
		IsDraft:             pr.Draft,
		Merged:              pr.Merged,
		MergeCommitOID:      deref(pr.MergeCommitSHA),
		Mergeable:           mergeableState(pr.Mergeable),
		MergeStateStatus:    mergeStateStatus(pr.MergeableState),
		Author:              b.gqlActor(pr.User, format),
		AuthorAssociation:   GQLAuthorAssociation(owner, pr.User),
		BaseRefName:         pr.Base.Ref,
		HeadRefName:         pr.Head.Ref,
		BaseRefOid:          gqlmodel.GitObjectID(pr.Base.SHA),
		HeadRefOid:          gqlmodel.GitObjectID(pr.Head.SHA),
		MaintainerCanModify: pr.MaintainerCanModify,
		FullDatabaseID:      &fullID,
		MergedBy:            b.gqlActor(pr.MergedBy, format),
		Additions:           int32(pr.Additions),
		Deletions:           int32(pr.Deletions),
		ChangedFiles:        int32(pr.ChangedFiles),
		CreatedAt:           gqlmodel.NewDateTime(pr.CreatedAt),
		UpdatedAt:           gqlmodel.NewDateTime(pr.UpdatedAt),
		ReactionGroups:      []gqlmodel.ReactionGroup{}, // Githome does not store reactions
		RepoOwner:           owner,
		RepoName:            repo,
		CommentsCount:       int32(pr.CommentsCount),
		CommitsCount:        int32(pr.CommitsCount),
	}
	out.Labels = b.gqlLabelConnection(owner, repo, pr.Labels, format)
	out.Assignees = b.GQLUserConnection(pr.Assignees, format)
	out.Milestone = b.GQLMilestone(owner, repo, pr.Milestone, format)
	basePK := pr.RepoPK
	if pr.Base.Repo != nil {
		basePK = pr.Base.Repo.PK
	}
	headPK := pr.RepoPK
	if pr.Head.Repo != nil {
		headPK = pr.Head.Repo.PK
	}
	out.IsCrossRepository = headPK != basePK
	baseID, headID := int64(0), int64(0)
	if pr.Repo != nil {
		baseID, headID = pr.Repo.ID, pr.Repo.ID
	}
	if pr.Base.Repo != nil {
		baseID = pr.Base.Repo.ID
	}
	if pr.Head.Repo != nil {
		headID = pr.Head.Repo.ID
	}
	if pr.Base.Ref != "" {
		out.BaseRef = GQLRef(baseID, "refs/heads/"+pr.Base.Ref, pr.Base.Ref, pr.Base.SHA)
	}
	if pr.Head.Ref != "" {
		out.HeadRef = GQLRef(headID, "refs/heads/"+pr.Head.Ref, pr.Head.Ref, pr.Head.SHA)
	}
	headRepo := pr.Head.Repo
	if headRepo == nil && headPK == pr.RepoPK {
		headRepo = pr.Repo
	}
	if headRepo != nil && headRepo.Owner != nil {
		hr := b.GQLRepository(headRepo, nil, format)
		out.HeadRepository = &hr
		out.HeadRepositoryOwner = b.GQLRepositoryOwner(headRepo.Owner, format)
	}
	if out.HeadRepositoryOwner == nil && pr.Head.User != nil {
		out.HeadRepositoryOwner = b.GQLRepositoryOwner(pr.Head.User, format)
	}
	if pr.MergedAt != nil {
		merged := gqlmodel.NewDateTime(*pr.MergedAt)
		out.MergedAt = &merged
	}
	if pr.ClosedAt != nil {
		closed := gqlmodel.NewDateTime(*pr.ClosedAt)
		out.ClosedAt = &closed
	}
	return out
}

// GQLPullRequestChangedFile renders a git file change into the GraphQL changed
// file shape.
func (b *URLBuilder) GQLPullRequestChangedFile(f git.FileChange) *gqlmodel.PullRequestChangedFile {
	return &gqlmodel.PullRequestChangedFile{
		Path:       f.Path,
		Additions:  int32(f.Additions),
		Deletions:  int32(f.Deletions),
		ChangeType: patchStatus(f.Status),
	}
}

// GQLPullRequestCommit renders a git commit into the GraphQL pull request commit
// shape, the {url, commit} wrapper GitHub nests a commit in.
func (b *URLBuilder) GQLPullRequestCommit(owner, repo string, c git.Commit) *gqlmodel.PullRequestCommit {
	return &gqlmodel.PullRequestCommit{
		URL: gqlmodel.URI(b.RepoHTML(owner, repo) + "/commit/" + c.SHA),
		Commit: &gqlmodel.Commit{
			Oid:             gqlmodel.GitObjectID(c.SHA),
			Message:         c.Message,
			MessageHeadline: messageHeadline(c.Message),
			RepoOwner:       owner,
			RepoName:        repo,
		},
	}
}

// pullState maps a domain pull request to the GraphQL state enum. A merged pull
// request reports MERGED even though its issue is closed, the distinction GitHub
// draws between a closed and a merged pull request.
func pullState(pr *domain.PullRequest) gqlmodel.PullRequestState {
	switch {
	case pr.Merged:
		return gqlmodel.PullRequestStateMerged
	case pr.State == "closed":
		return gqlmodel.PullRequestStateClosed
	default:
		return gqlmodel.PullRequestStateOpen
	}
}

// mergeableState maps the tri-state domain mergeable to the GraphQL enum: a nil
// value is UNKNOWN, the state the worker has not yet resolved.
func mergeableState(m *bool) gqlmodel.MergeableState {
	switch {
	case m == nil:
		return gqlmodel.MergeableStateUnknown
	case *m:
		return gqlmodel.MergeableStateMergeable
	default:
		return gqlmodel.MergeableStateConflicting
	}
}

// mergeStateStatus maps the domain mergeable_state string to the GraphQL enum,
// defaulting an unrecognized or empty value to UNKNOWN.
func mergeStateStatus(state string) gqlmodel.MergeStateStatus {
	switch state {
	case "clean":
		return gqlmodel.MergeStateStatusClean
	case "dirty":
		return gqlmodel.MergeStateStatusDirty
	case "behind":
		return gqlmodel.MergeStateStatusBehind
	case "draft":
		return gqlmodel.MergeStateStatusDraft
	case "blocked":
		return gqlmodel.MergeStateStatusBlocked
	case "unstable":
		return gqlmodel.MergeStateStatusUnstable
	case "has_hooks":
		return gqlmodel.MergeStateStatusHasHooks
	default:
		return gqlmodel.MergeStateStatusUnknown
	}
}

// patchStatus maps the git file-change status to the GraphQL PatchStatus enum.
func patchStatus(status string) gqlmodel.PatchStatus {
	switch status {
	case "added":
		return gqlmodel.PatchStatusAdded
	case "removed":
		return gqlmodel.PatchStatusDeleted
	case "renamed":
		return gqlmodel.PatchStatusRenamed
	case "copied":
		return gqlmodel.PatchStatusCopied
	case "changed":
		return gqlmodel.PatchStatusChanged
	default:
		return gqlmodel.PatchStatusModified
	}
}

// GQLCommit renders a git commit into the GraphQL Commit shape. repoDBID is
// the repository's public database id the node id encodes; owner and name are
// the coordinates the statusCheckRollup resolver reads back.
func GQLCommit(repoDBID int64, owner, name string, c git.Commit) *gqlmodel.Commit {
	return &gqlmodel.Commit{
		ID:              nodeid.EncodeGitObject("commit", repoDBID, string(c.SHA)),
		Oid:             gqlmodel.GitObjectID(c.SHA),
		Message:         c.Message,
		MessageHeadline: messageHeadline(c.Message),
		RepoOwner:       owner,
		RepoName:        name,
	}
}

// messageHeadline is the first line of a commit message, the headline GitHub
// splits from the body.
func messageHeadline(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		return message[:i]
	}
	return message
}
