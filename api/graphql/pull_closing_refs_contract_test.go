package graphql_test

import (
	"encoding/json"
	"testing"

	"github.com/tamnd/githome/domain"
)

// The PullRequest fields R02-14 adds: the merge commit and its speculative twin
// (null while unmerged), and the closing-issue references gh and the web UI read
// from the body's closing keywords.
const pullClosingRefsQuery = `query ClosingRefs($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      mergeCommit { oid }
      potentialMergeCommit { oid }
      closingIssuesReferences(first: 10) {
        totalCount
        nodes { number title }
      }
    }
  }
}`

// TestPullClosingIssuesReferences confirms closingIssuesReferences derives its
// connection from the pull request body's closing keywords: an unkeyworded body
// resolves an empty connection, and a body carrying "Closes #2" resolves the
// referenced issue. mergeCommit and potentialMergeCommit stay null while the
// pull request is open, the GitHub-faithful null rather than a fabricated commit.
func TestPullClosingIssuesReferences(t *testing.T) {
	fx := pullServer(t)
	vars := map[string]any{"owner": "octocat", "name": "hello", "number": 1}

	// The seeded body ("It adds a feature.") carries no closing keyword.
	got := post(t, fx.srv, fx.token, pullClosingRefsQuery, vars)
	mergeOID, potentialOID, refs := closingRefsFields(t, got)
	if mergeOID != "" || potentialOID != "" {
		t.Fatalf("open pull request mergeCommit=%q potentialMergeCommit=%q, want both null, body %s", mergeOID, potentialOID, got)
	}
	if refs.TotalCount != 0 || len(refs.Nodes) != 0 {
		t.Fatalf("unkeyworded body resolved %d closing refs, want 0, body %s", refs.TotalCount, got)
	}

	// Add a closing keyword referencing issue #2, then re-query.
	body := "Closes #2"
	if _, err := fx.pulls.UpdatePR(fx.ctx, fx.ownerPK, "octocat", "hello", 1, domain.PRPatch{Body: &body}); err != nil {
		t.Fatalf("UpdatePR body: %v", err)
	}

	got = post(t, fx.srv, fx.token, pullClosingRefsQuery, vars)
	_, _, refs = closingRefsFields(t, got)
	if refs.TotalCount != 1 || len(refs.Nodes) != 1 {
		t.Fatalf("keyworded body resolved %d closing refs, want 1, body %s", refs.TotalCount, got)
	}
	if refs.Nodes[0].Number != 2 {
		t.Fatalf("closing ref = #%d, want #2, body %s", refs.Nodes[0].Number, got)
	}
}

// closingRefsFields pulls the merge commit oids and the closing-issue connection
// out of a closingIssuesReferences response.
func closingRefsFields(t *testing.T, body []byte) (string, string, struct {
	TotalCount int
	Nodes      []struct {
		Number int
		Title  string
	}
}) {
	t.Helper()
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Repository struct {
				PullRequest struct {
					MergeCommit          *struct{ Oid string } `json:"mergeCommit"`
					PotentialMergeCommit *struct{ Oid string } `json:"potentialMergeCommit"`
					ClosingIssues        struct {
						TotalCount int `json:"totalCount"`
						Nodes      []struct {
							Number int    `json:"number"`
							Title  string `json:"title"`
						} `json:"nodes"`
					} `json:"closingIssuesReferences"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal closing refs: %v, body %s", err, body)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("closing refs errors: %v", env.Errors)
	}
	pr := env.Data.Repository.PullRequest
	mergeOID, potentialOID := "", ""
	if pr.MergeCommit != nil {
		mergeOID = pr.MergeCommit.Oid
	}
	if pr.PotentialMergeCommit != nil {
		potentialOID = pr.PotentialMergeCommit.Oid
	}
	refs := struct {
		TotalCount int
		Nodes      []struct {
			Number int
			Title  string
		}
	}{TotalCount: pr.ClosingIssues.TotalCount}
	for _, n := range pr.ClosingIssues.Nodes {
		refs.Nodes = append(refs.Nodes, struct {
			Number int
			Title  string
		}{Number: n.Number, Title: n.Title})
	}
	return mergeOID, potentialOID, refs
}
