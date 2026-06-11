package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// PullRequest renders the full pull request object for owner/repo. It is pure:
// the same domain pull request, URL config, and node-id format always produce the
// same bytes. detail controls the merge view: the single-pull endpoint passes
// true to fill merged, the mergeable triplet, and the diff stats, while the list
// endpoint passes false so those fields drop out, matching GitHub where a listed
// pull request carries only its summary. The REST id is the issue row's id, the
// shared id space a pull request and its issue occupy; the node id encodes the
// pull request's own id under the PullRequest kind.
func (b *URLBuilder) PullRequest(owner, repo string, pr *domain.PullRequest, format nodeid.Format, detail bool) restmodel.PullRequest {
	base := b.RepoAPI(owner, repo)
	num := strconv.FormatInt(pr.Number, 10)
	self := base + "/pulls/" + num
	html := b.RepoHTML(owner, repo) + "/pull/" + num
	issueURL := base + "/issues/" + num

	out := restmodel.PullRequest{
		URL:                self,
		ID:                 pr.IssueID,
		NodeID:             nodeid.Encode(nodeid.KindPullRequest, pr.ID, format),
		HTMLURL:            html,
		DiffURL:            html + ".diff",
		PatchURL:           html + ".patch",
		IssueURL:           issueURL,
		CommitsURL:         self + "/commits",
		ReviewCommentsURL:  self + "/comments",
		ReviewCommentURL:   base + "/pulls/comments{/number}",
		CommentsURL:        issueURL + "/comments",
		StatusesURL:        base + "/statuses/" + pr.Head.SHA,
		Number:             pr.Number,
		State:              pr.State,
		Locked:             pr.Locked,
		ActiveLockReason:   pr.ActiveLockReason,
		Title:              pr.Title,
		User:               b.SimpleUser(pr.User, format),
		Body:               pr.Body,
		Labels:             b.labels(owner, repo, pr.Labels, format),
		Milestone:          b.milestonePtr(owner, repo, pr.Milestone, format),
		CreatedAt:          restmodel.NewTime(pr.CreatedAt),
		UpdatedAt:          restmodel.NewTime(pr.UpdatedAt),
		ClosedAt:           timePtr(pr.ClosedAt),
		MergedAt:           timePtr(pr.MergedAt),
		MergeCommitSHA:     pr.MergeCommitSHA,
		Assignees:          b.assignees(pr.Assignees, format),
		RequestedReviewers: b.assignees(pr.RequestedReviewers, format),
		RequestedTeams:     []any{},
		Head:               b.pullRef(pr.Head, format),
		Base:               b.pullRef(pr.Base, format),
		Links:              pullLinks(self, html, issueURL),
		AuthorAssociation:  authorAssociation(pr.User.Login, owner),
		Draft:              pr.Draft,
	}
	if len(out.Assignees) > 0 {
		first := out.Assignees[0]
		out.Assignee = &first
	}
	if !detail {
		return out
	}

	// The single-pull view carries the merge state and the diff stats. The
	// mergeable triplet stays null until the worker resolves it.
	merged := pr.Merged
	comments := pr.CommentsCount
	reviewComments := 0
	commits := pr.CommitsCount
	additions := pr.Additions
	deletions := pr.Deletions
	changed := pr.ChangedFiles
	out.Merged = &merged
	out.Mergeable = pr.Mergeable
	out.Rebaseable = pr.Rebaseable
	out.MergeableState = pr.MergeableState
	out.Comments = &comments
	out.ReviewComments = &reviewComments
	out.Commits = &commits
	out.Additions = &additions
	out.Deletions = &deletions
	out.ChangedFiles = &changed
	if pr.MergedBy != nil {
		mb := b.SimpleUser(pr.MergedBy, format)
		out.MergedBy = &mb
	}
	return out
}

// PullRequestFile renders one element of the files endpoint from a git file
// change. The blob and raw URLs hang off the head sha so they address the file as
// the pull request leaves it; the contents URL points at the same ref.
func (b *URLBuilder) PullRequestFile(owner, repo, headSHA string, f git.FileChange) restmodel.PullRequestFile {
	htmlRepo := b.RepoHTML(owner, repo)
	out := restmodel.PullRequestFile{
		SHA:         f.SHA,
		Filename:    f.Path,
		Status:      f.Status,
		Additions:   f.Additions,
		Deletions:   f.Deletions,
		Changes:     f.Additions + f.Deletions,
		BlobURL:     htmlRepo + "/blob/" + headSHA + "/" + f.Path,
		RawURL:      htmlRepo + "/raw/" + headSHA + "/" + f.Path,
		ContentsURL: b.RepoAPI(owner, repo) + "/contents/" + f.Path + "?ref=" + headSHA,
		Patch:       f.Patch,
	}
	if f.PrevPath != "" {
		prev := f.PrevPath
		out.PreviousFilename = &prev
	}
	return out
}

// PullRequestMergeResult renders the body of a successful merge.
func (b *URLBuilder) PullRequestMergeResult(r *domain.MergeResult) restmodel.PullRequestMergeResult {
	return restmodel.PullRequestMergeResult{SHA: r.SHA, Merged: r.Merged, Message: r.Message}
}

// pullRef renders one side of a pull request. The endpoint's repository is
// rendered when present; a head whose fork vanished leaves it null.
func (b *URLBuilder) pullRef(e domain.GitEndpoint, format nodeid.Format) restmodel.PullRequestRef {
	out := restmodel.PullRequestRef{
		Label: e.Label,
		Ref:   e.Ref,
		SHA:   e.SHA,
	}
	if e.User != nil {
		u := b.SimpleUser(e.User, format)
		out.User = &u
	}
	if e.Repo != nil {
		r := b.Repository(e.Repo, format, nil)
		out.Repo = &r
	}
	return out
}

// pullLinks builds the _links block from the pull request's own URLs.
func pullLinks(self, html, issueURL string) restmodel.PullRequestLinks {
	return restmodel.PullRequestLinks{
		Self:           restmodel.Link{HRef: self},
		HTML:           restmodel.Link{HRef: html},
		Issue:          restmodel.Link{HRef: issueURL},
		Comments:       restmodel.Link{HRef: issueURL + "/comments"},
		ReviewComments: restmodel.Link{HRef: self + "/comments"},
		ReviewComment:  restmodel.Link{HRef: self + "/comments{/number}"},
		Commits:        restmodel.Link{HRef: self + "/commits"},
		Statuses:       restmodel.Link{HRef: self + "/statuses"},
	}
}
