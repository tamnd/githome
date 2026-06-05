package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// Review renders one review object for owner/repo. The node id encodes the
// review's own id under the PullRequestReview kind; the html anchor and the pull
// request url hang off the pull request number the review belongs to.
func (b *URLBuilder) Review(owner, repo string, r *domain.Review, format nodeid.Format) restmodel.Review {
	num := strconv.FormatInt(r.PullNumber, 10)
	pullURL := b.RepoAPI(owner, repo) + "/pulls/" + num
	html := b.RepoHTML(owner, repo) + "/pull/" + num + "#pullrequestreview-" + strconv.FormatInt(r.ID, 10)
	return restmodel.Review{
		ID:             r.ID,
		NodeID:         nodeid.Encode(nodeid.KindPullRequestReview, r.ID, format),
		User:           b.SimpleUser(r.User, format),
		Body:           r.Body,
		State:          r.State,
		HTMLURL:        html,
		PullRequestURL: pullURL,
		Links: restmodel.ReviewLinks{
			HTML:        restmodel.Link{HRef: html},
			PullRequest: restmodel.Link{HRef: pullURL},
		},
		SubmittedAt:       timePtr(r.SubmittedAt),
		CommitID:          r.CommitID,
		AuthorAssociation: authorAssociation(r.User.Login, owner),
	}
}

// ReviewComment renders one inline comment for owner/repo. Both the line/side
// anchor and the legacy position are rendered; original_* mirror the values as the
// comment was first written.
func (b *URLBuilder) ReviewComment(owner, repo string, c *domain.ReviewComment, format nodeid.Format) restmodel.ReviewComment {
	num := strconv.FormatInt(c.PullNumber, 10)
	id := strconv.FormatInt(c.ID, 10)
	self := b.RepoAPI(owner, repo) + "/pulls/comments/" + id
	pullURL := b.RepoAPI(owner, repo) + "/pulls/" + num
	html := b.RepoHTML(owner, repo) + "/pull/" + num + "#discussion_r" + id
	subjectType := c.SubjectType
	if subjectType == "" {
		subjectType = "line"
	}
	return restmodel.ReviewComment{
		URL:                 self,
		PullRequestReviewID: c.ReviewID,
		ID:                  c.ID,
		NodeID:              nodeid.Encode(nodeid.KindPullRequestReviewComment, c.ID, format),
		DiffHunk:            c.DiffHunk,
		Path:                c.Path,
		Position:            c.Position,
		OriginalPosition:    c.OriginalPosition,
		CommitID:            c.CommitID,
		OriginalCommitID:    c.OriginalCommitID,
		InReplyToID:         c.InReplyTo,
		User:                b.SimpleUser(c.User, format),
		Body:                c.Body,
		CreatedAt:           restmodel.NewTime(c.CreatedAt),
		UpdatedAt:           restmodel.NewTime(c.UpdatedAt),
		HTMLURL:             html,
		PullRequestURL:      pullURL,
		AuthorAssociation:   authorAssociation(c.User.Login, owner),
		Links: restmodel.ReviewCommentLinks{
			Self:        restmodel.Link{HRef: self},
			HTML:        restmodel.Link{HRef: html},
			PullRequest: restmodel.Link{HRef: pullURL},
		},
		StartLine:         c.StartLine,
		OriginalStartLine: c.StartLine,
		StartSide:         c.StartSide,
		Line:              c.Line,
		OriginalLine:      c.Line,
		Side:              c.Side,
		SubjectType:       subjectType,
	}
}
