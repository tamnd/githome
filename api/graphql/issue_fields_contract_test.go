package graphql_test

import (
	"encoding/json"
	"testing"
)

// TestIssueRemainderFields covers R02-16: an Issue serves databaseId,
// activeLockReason, isPinned, viewerCanUpdate, reactionGroups, and an assignees
// connection that carries pageInfo.
func TestIssueRemainderFields(t *testing.T) {
	srv, token := issueServer(t)

	q := `query($owner:String!,$name:String!,$number:Int!){
	  repository(owner:$owner,name:$name){
	    issue(number:$number){
	      databaseId
	      activeLockReason
	      isPinned
	      viewerCanUpdate
	      reactionGroups { content users { totalCount } }
	      assignees(first: 10) { totalCount pageInfo { hasNextPage } nodes { login } }
	    }
	  }
	}`
	got := post(t, srv, token, q, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	var env struct {
		Data struct {
			Repository struct {
				Issue struct {
					DatabaseID       *int    `json:"databaseId"`
					ActiveLockReason *string `json:"activeLockReason"`
					IsPinned         bool    `json:"isPinned"`
					ViewerCanUpdate  bool    `json:"viewerCanUpdate"`
					ReactionGroups   []struct {
						Content string `json:"content"`
					} `json:"reactionGroups"`
					Assignees struct {
						TotalCount int `json:"totalCount"`
						PageInfo   struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
						Nodes []struct {
							Login string `json:"login"`
						} `json:"nodes"`
					} `json:"assignees"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, body %s", env.Errors, got)
	}
	iss := env.Data.Repository.Issue

	if iss.DatabaseID == nil || *iss.DatabaseID <= 0 {
		t.Errorf("databaseId = %v, want a positive db id", iss.DatabaseID)
	}
	if iss.ActiveLockReason != nil {
		t.Errorf("activeLockReason = %v, want null on an unlocked issue", *iss.ActiveLockReason)
	}
	if iss.IsPinned {
		t.Errorf("isPinned = true, want false")
	}
	// The token belongs to octocat, who owns the repo and authored the issue, so
	// the viewer can update it.
	if !iss.ViewerCanUpdate {
		t.Errorf("viewerCanUpdate = false, want true for the owner/author")
	}
	if iss.ReactionGroups == nil {
		t.Errorf("reactionGroups = null, want a (possibly empty) array")
	}
	// assignees must carry pageInfo (R02-16): the connection used to omit it.
	if iss.Assignees.PageInfo.HasNextPage {
		t.Errorf("assignees.pageInfo.hasNextPage = true, want false for a full single page")
	}
}
