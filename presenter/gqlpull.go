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
	out := &gqlmodel.PullRequest{
		ID:               nodeid.Encode(nodeid.KindPullRequest, pr.ID, format),
		Number:           int32(pr.Number),
		Title:            pr.Title,
		Body:             deref(pr.Body),
		State:            pullState(pr),
		URL:              gqlmodel.URI(b.RepoHTML(owner, repo) + "/pull/" + num),
		Locked:           pr.Locked,
		Closed:           pr.State == "closed",
		IsDraft:          pr.Draft,
		Merged:           pr.Merged,
		Mergeable:        mergeableState(pr.Mergeable),
		MergeStateStatus: mergeStateStatus(pr.MergeableState),
		Author:           b.gqlActor(pr.User, format),
		BaseRefName:      pr.Base.Ref,
		HeadRefName:      pr.Head.Ref,
		BaseRefOid:       gqlmodel.GitObjectID(pr.Base.SHA),
		HeadRefOid:       gqlmodel.GitObjectID(pr.Head.SHA),
		Additions:        int32(pr.Additions),
		Deletions:        int32(pr.Deletions),
		ChangedFiles:     int32(pr.ChangedFiles),
		CreatedAt:        gqlmodel.NewDateTime(pr.CreatedAt),
		UpdatedAt:        gqlmodel.NewDateTime(pr.UpdatedAt),
		RepoOwner:        owner,
		RepoName:         repo,
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
	if pr.Base.Ref != "" {
		out.BaseRef = GQLRef(basePK, "refs/heads/"+pr.Base.Ref, pr.Base.Ref, pr.Base.SHA)
	}
	if pr.Head.Ref != "" {
		out.HeadRef = GQLRef(headPK, "refs/heads/"+pr.Head.Ref, pr.Head.Ref, pr.Head.SHA)
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

// messageHeadline is the first line of a commit message, the headline GitHub
// splits from the body.
func messageHeadline(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		return message[:i]
	}
	return message
}
