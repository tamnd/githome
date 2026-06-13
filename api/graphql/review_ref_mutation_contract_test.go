package graphql_test

import (
	"encoding/json"
	"testing"
)

const reviewIDQuery = `query($o:String!,$n:String!,$num:Int!){
  repository(owner:$o,name:$n){ pullRequest(number:$num){
    reviews(first:10){ nodes{ id state author{ login } } }
  } }
}`

const reviewCommentIDQuery = `query($o:String!,$n:String!,$num:Int!){
  repository(owner:$o,name:$n){ pullRequest(number:$num){
    reviewThreads(first:10){ nodes{ comments(first:10){ nodes{ id body } } } }
  } }
}`

const dismissReviewMutation = `mutation($id:ID!,$msg:String!){
  dismissPullRequestReview(input:{pullRequestReviewId:$id, message:$msg}){
    pullRequestReview{ id state }
  }
}`

const updateReviewCommentMutation = `mutation($id:ID!,$body:String!){
  updatePullRequestReviewComment(input:{pullRequestReviewCommentId:$id, body:$body}){
    pullRequestReviewComment{ id body }
  }
}`

// firstReviewID reads the node id and state of the first review on the pull
// request, the review a maintainer dismisses.
func firstReviewID(t *testing.T, got []byte) string {
	t.Helper()
	var env struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Reviews struct {
						Nodes []struct {
							ID    string `json:"id"`
							State string `json:"state"`
						} `json:"nodes"`
					} `json:"reviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal review id: %v\n%s", err, got)
	}
	nodes := env.Data.Repository.PullRequest.Reviews.Nodes
	if len(nodes) == 0 || nodes[0].ID == "" {
		t.Fatalf("no review id on pull request: %s", got)
	}
	return nodes[0].ID
}

// firstReviewCommentID reads the node id of the first inline review comment.
func firstReviewCommentID(t *testing.T, got []byte) string {
	t.Helper()
	var env struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							Comments struct {
								Nodes []struct {
									ID string `json:"id"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal review comment id: %v\n%s", err, got)
	}
	for _, th := range env.Data.Repository.PullRequest.ReviewThreads.Nodes {
		for _, c := range th.Comments.Nodes {
			if c.ID != "" {
				return c.ID
			}
		}
	}
	t.Fatalf("no review comment id on pull request: %s", got)
	return ""
}

// TestDismissPullRequestReview confirms dismissPullRequestReview moves the
// seeded approval to DISMISSED, the mutation gh pr review --dismiss sends. The
// repository owner dismisses the reviewer's approval.
func TestDismissPullRequestReview(t *testing.T) {
	fx := reviewGraphQLServer(t)
	got := post(t, fx.srv, fx.ownerToken, reviewIDQuery, map[string]any{"o": "octocat", "n": "hello", "num": 1})
	reviewID := firstReviewID(t, got)

	got = post(t, fx.srv, fx.ownerToken, dismissReviewMutation, map[string]any{"id": reviewID, "msg": "Please rebase."})
	var env struct {
		Data struct {
			DismissPullRequestReview struct {
				PullRequestReview struct {
					State string `json:"state"`
				} `json:"pullRequestReview"`
			} `json:"dismissPullRequestReview"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal dismiss: %v\n%s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("dismissPullRequestReview errors: %v\n%s", env.Errors, got)
	}
	if env.Data.DismissPullRequestReview.PullRequestReview.State != "DISMISSED" {
		t.Errorf("review state = %q, want DISMISSED\n%s",
			env.Data.DismissPullRequestReview.PullRequestReview.State, got)
	}
}

// TestUpdatePullRequestReviewComment confirms updatePullRequestReviewComment
// edits an inline review comment's body. The reviewer authored the comment, so
// they edit it.
func TestUpdatePullRequestReviewComment(t *testing.T) {
	fx := reviewGraphQLServer(t)
	got := post(t, fx.srv, fx.reviewToken, reviewCommentIDQuery, map[string]any{"o": "octocat", "n": "hello", "num": 1})
	commentID := firstReviewCommentID(t, got)

	got = post(t, fx.srv, fx.reviewToken, updateReviewCommentMutation, map[string]any{"id": commentID, "body": "Reworded note."})
	var env struct {
		Data struct {
			UpdatePullRequestReviewComment struct {
				PullRequestReviewComment struct {
					Body string `json:"body"`
				} `json:"pullRequestReviewComment"`
			} `json:"updatePullRequestReviewComment"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal update review comment: %v\n%s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("updatePullRequestReviewComment errors: %v\n%s", env.Errors, got)
	}
	if env.Data.UpdatePullRequestReviewComment.PullRequestReviewComment.Body != "Reworded note." {
		t.Errorf("body = %q, want %q\n%s",
			env.Data.UpdatePullRequestReviewComment.PullRequestReviewComment.Body, "Reworded note.", got)
	}
}

const refQuery = `query($o:String!,$n:String!,$ref:String!){
  repository(owner:$o,name:$n){ ref(qualifiedName:$ref){ id target{ oid } } }
}`

const updateRefMutation = `mutation($id:ID!,$oid:GitObjectID!){
  updateRef(input:{refId:$id, oid:$oid, force:true}){ ref{ name target{ oid } } }
}`

// refIDAndOid reads a ref's node id and its target oid.
func refIDAndOid(t *testing.T, got []byte) (string, string) {
	t.Helper()
	var env struct {
		Data struct {
			Repository struct {
				Ref struct {
					ID     string `json:"id"`
					Target struct {
						Oid string `json:"oid"`
					} `json:"target"`
				} `json:"ref"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal ref: %v\n%s", err, got)
	}
	return env.Data.Repository.Ref.ID, env.Data.Repository.Ref.Target.Oid
}

// TestUpdateRef confirms updateRef repoints an existing branch at a new commit,
// the mutation gh's branch-sync path sends. It force-moves feature onto main's
// tip and reads the new target back.
func TestUpdateRef(t *testing.T) {
	fx := reviewGraphQLServer(t)
	got := post(t, fx.srv, fx.ownerToken, refQuery, map[string]any{"o": "octocat", "n": "hello", "ref": "refs/heads/main"})
	_, mainOid := refIDAndOid(t, got)
	got = post(t, fx.srv, fx.ownerToken, refQuery, map[string]any{"o": "octocat", "n": "hello", "ref": "refs/heads/feature"})
	featureID, featureOid := refIDAndOid(t, got)
	if featureID == "" || mainOid == "" {
		t.Fatalf("missing feature id (%q) or main oid (%q)", featureID, mainOid)
	}
	if mainOid == featureOid {
		t.Fatalf("feature already at main's tip; cannot exercise a move")
	}

	got = post(t, fx.srv, fx.ownerToken, updateRefMutation, map[string]any{"id": featureID, "oid": mainOid})
	var env struct {
		Data struct {
			UpdateRef struct {
				Ref struct {
					Name   string `json:"name"`
					Target struct {
						Oid string `json:"oid"`
					} `json:"target"`
				} `json:"ref"`
			} `json:"updateRef"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal updateRef: %v\n%s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("updateRef errors: %v\n%s", env.Errors, got)
	}
	if env.Data.UpdateRef.Ref.Target.Oid != mainOid {
		t.Errorf("ref target oid = %q, want %q after force update\n%s",
			env.Data.UpdateRef.Ref.Target.Oid, mainOid, got)
	}
}

const updateBranchMutation = `mutation($id:ID!,$oid:GitObjectID){
  updatePullRequestBranch(input:{pullRequestId:$id, expectedHeadOid:$oid}){
    pullRequest{ number }
  }
}`

// TestUpdatePullRequestBranchGuards confirms updatePullRequestBranch is wired to
// the pull service: a wrong expectedHeadOid surfaces an error rather than a
// silent success, and a branch already current with its base reports honestly
// instead of pretending an update happened.
func TestUpdatePullRequestBranchGuards(t *testing.T) {
	fx := reviewGraphQLServer(t)
	prID := graphqlID(t, fx.srv, fx.ownerToken, prIDByNumberQuery,
		map[string]any{"owner": "octocat", "name": "hello", "number": 1}, "repository", "pullRequest", "id")

	// A stale expected head oid must be rejected, not ignored.
	got := post(t, fx.srv, fx.ownerToken, updateBranchMutation,
		map[string]any{"id": prID, "oid": "0000000000000000000000000000000000000000"})
	if errs := errorMessages(t, got); len(errs) == 0 {
		t.Fatalf("updatePullRequestBranch with a stale oid reported success: %s", got)
	}

	// With no base updates the feature branch is already current; the service
	// reports that rather than fabricating a merge.
	got = post(t, fx.srv, fx.ownerToken, updateBranchMutation, map[string]any{"id": prID})
	if errs := errorMessages(t, got); len(errs) == 0 {
		t.Fatalf("updatePullRequestBranch with no base updates reported success: %s", got)
	}
}
