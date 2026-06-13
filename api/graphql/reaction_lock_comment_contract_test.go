package graphql_test

import (
	"encoding/json"
	"testing"
)

const issueCommentIDQuery = `query($o:String!,$n:String!,$num:Int!){
  repository(owner:$o,name:$n){ issue(number:$num){ comments(first:1){ nodes{ id body } } } }
}`

const updateIssueCommentMutation = `mutation($id:ID!,$body:String!){
  updateIssueComment(input:{id:$id, body:$body}){ issueComment{ id body includesCreatedEdit } }
}`

const lockLockableMutation = `mutation($id:ID!,$reason:LockReason){
  lockLockable(input:{lockableId:$id, lockReason:$reason}){ lockedRecord{ ... on Issue{ number locked } } }
}`

const unlockLockableMutation = `mutation($id:ID!){
  unlockLockable(input:{lockableId:$id}){ unlockedRecord{ ... on Issue{ number locked } } }
}`

const addReactionMutation = `mutation($id:ID!,$c:ReactionContent!){
  addReaction(input:{subjectId:$id, content:$c}){
    reaction{ content }
    subject{ ... on Issue{ number } }
    reactionGroups{ content users{ totalCount } }
  }
}`

const removeReactionMutation = `mutation($id:ID!,$c:ReactionContent!){
  removeReaction(input:{subjectId:$id, content:$c}){
    reaction{ content }
    reactionGroups{ content users{ totalCount } }
  }
}`

// commentIDFromIssue reads the first comment node id off issue number.
func commentIDFromIssue(t *testing.T, got []byte) string {
	t.Helper()
	var env struct {
		Data struct {
			Repository struct {
				Issue struct {
					Comments struct {
						Nodes []struct {
							ID string `json:"id"`
						} `json:"nodes"`
					} `json:"comments"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal comment id: %v\n%s", err, got)
	}
	nodes := env.Data.Repository.Issue.Comments.Nodes
	if len(nodes) == 0 || nodes[0].ID == "" {
		t.Fatalf("no comment id on issue: %s", got)
	}
	return nodes[0].ID
}

// TestUpdateIssueComment confirms updateIssueComment edits the comment body and
// flags the edit, the mutation gh issue/pr comment --edit-last sends.
func TestUpdateIssueComment(t *testing.T) {
	srv, token := issueServer(t)
	got := post(t, srv, token, issueCommentIDQuery, map[string]any{"o": "octocat", "n": "hello", "num": 1})
	commentID := commentIDFromIssue(t, got)

	got = post(t, srv, token, updateIssueCommentMutation, map[string]any{"id": commentID, "body": "edited body"})
	var env struct {
		Data struct {
			UpdateIssueComment struct {
				IssueComment struct {
					Body                string `json:"body"`
					IncludesCreatedEdit bool   `json:"includesCreatedEdit"`
				} `json:"issueComment"`
			} `json:"updateIssueComment"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal updateIssueComment: %v\n%s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("updateIssueComment errors: %v\n%s", env.Errors, got)
	}
	if env.Data.UpdateIssueComment.IssueComment.Body != "edited body" {
		t.Errorf("body = %q, want %q\n%s", env.Data.UpdateIssueComment.IssueComment.Body, "edited body", got)
	}
	if !env.Data.UpdateIssueComment.IssueComment.IncludesCreatedEdit {
		t.Errorf("includesCreatedEdit = false, want true after an edit\n%s", got)
	}
}

// TestLockUnlockLockable confirms lockLockable and unlockLockable flip an
// issue's locked state and report the affected record, the mutations gh issue
// lock/unlock send.
func TestLockUnlockLockable(t *testing.T) {
	srv, token := issueServer(t)
	issueID := issueNodeID(t, srv, token, 1)

	got := post(t, srv, token, lockLockableMutation, map[string]any{"id": issueID, "reason": "OFF_TOPIC"})
	var locked struct {
		Data struct {
			LockLockable struct {
				LockedRecord struct {
					Number int  `json:"number"`
					Locked bool `json:"locked"`
				} `json:"lockedRecord"`
			} `json:"lockLockable"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &locked); err != nil {
		t.Fatalf("unmarshal lockLockable: %v\n%s", err, got)
	}
	if len(locked.Errors) != 0 {
		t.Fatalf("lockLockable errors: %v\n%s", locked.Errors, got)
	}
	if !locked.Data.LockLockable.LockedRecord.Locked {
		t.Errorf("lockedRecord.locked = false, want true\n%s", got)
	}

	got = post(t, srv, token, unlockLockableMutation, map[string]any{"id": issueID})
	var unlocked struct {
		Data struct {
			UnlockLockable struct {
				UnlockedRecord struct {
					Locked bool `json:"locked"`
				} `json:"unlockedRecord"`
			} `json:"unlockLockable"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &unlocked); err != nil {
		t.Fatalf("unmarshal unlockLockable: %v\n%s", err, got)
	}
	if len(unlocked.Errors) != 0 {
		t.Fatalf("unlockLockable errors: %v\n%s", unlocked.Errors, got)
	}
	if unlocked.Data.UnlockLockable.UnlockedRecord.Locked {
		t.Errorf("unlockedRecord.locked = true, want false after unlock\n%s", got)
	}
}

// reactionGroupShape decodes an addReaction/removeReaction payload's groups.
type reactionPayload struct {
	Reaction struct {
		Content string `json:"content"`
	} `json:"reaction"`
	ReactionGroups []struct {
		Content string `json:"content"`
		Users   struct {
			TotalCount int `json:"totalCount"`
		} `json:"users"`
	} `json:"reactionGroups"`
}

// TestAddRemoveReaction confirms addReaction records the viewer's emoji on an
// issue and reports the updated rollup, and removeReaction takes it back off,
// keyed on subject and content the way GitHub's API works.
func TestAddRemoveReaction(t *testing.T) {
	srv, token := issueServer(t)
	issueID := issueNodeID(t, srv, token, 2)

	got := post(t, srv, token, addReactionMutation, map[string]any{"id": issueID, "c": "THUMBS_UP"})
	var added struct {
		Data struct {
			AddReaction reactionPayload `json:"addReaction"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &added); err != nil {
		t.Fatalf("unmarshal addReaction: %v\n%s", err, got)
	}
	if len(added.Errors) != 0 {
		t.Fatalf("addReaction errors: %v\n%s", added.Errors, got)
	}
	if added.Data.AddReaction.Reaction.Content != "THUMBS_UP" {
		t.Errorf("reaction.content = %q, want THUMBS_UP\n%s", added.Data.AddReaction.Reaction.Content, got)
	}
	foundGroup := false
	for _, g := range added.Data.AddReaction.ReactionGroups {
		if g.Content == "THUMBS_UP" && g.Users.TotalCount == 1 {
			foundGroup = true
		}
	}
	if !foundGroup {
		t.Errorf("reactionGroups missing THUMBS_UP/1 after add: %s", got)
	}

	got = post(t, srv, token, removeReactionMutation, map[string]any{"id": issueID, "c": "THUMBS_UP"})
	var removed struct {
		Data struct {
			RemoveReaction reactionPayload `json:"removeReaction"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &removed); err != nil {
		t.Fatalf("unmarshal removeReaction: %v\n%s", err, got)
	}
	if len(removed.Errors) != 0 {
		t.Fatalf("removeReaction errors: %v\n%s", removed.Errors, got)
	}
	for _, g := range removed.Data.RemoveReaction.ReactionGroups {
		if g.Content == "THUMBS_UP" && g.Users.TotalCount > 0 {
			t.Errorf("THUMBS_UP still present after remove: %s", got)
		}
	}
}

const deleteIssueMutation = `mutation($id:ID!){ deleteIssue(input:{issueId:$id}){ repository{ id } } }`
const transferIssueMutation = `mutation($id:ID!,$repo:ID!){ transferIssue(input:{issueId:$id, repositoryId:$repo}){ issue{ number } } }`
const pinIssueMutation = `mutation($id:ID!){ pinIssue(input:{issueId:$id}){ issue{ number } } }`
const unpinIssueMutation = `mutation($id:ID!){ unpinIssue(input:{issueId:$id}){ issue{ number } } }`

// TestUnsupportedIssueMutationsErrorHonestly confirms R02-26: deleteIssue,
// transferIssue, pinIssue, and unpinIssue report UNPROCESSABLE rather than a
// fake success, since Githome does not model those operations.
func TestUnsupportedIssueMutationsErrorHonestly(t *testing.T) {
	srv, token := issueServer(t)
	issueID := issueNodeID(t, srv, token, 1)
	repoID := repoNodeID(t, srv, token)

	cases := []struct {
		name string
		mut  string
		vars map[string]any
	}{
		{"deleteIssue", deleteIssueMutation, map[string]any{"id": issueID}},
		{"transferIssue", transferIssueMutation, map[string]any{"id": issueID, "repo": repoID}},
		{"pinIssue", pinIssueMutation, map[string]any{"id": issueID}},
		{"unpinIssue", unpinIssueMutation, map[string]any{"id": issueID}},
	}
	for _, c := range cases {
		got := post(t, srv, token, c.mut, c.vars)
		errs := errorMessages(t, got)
		if len(errs) == 0 || errs[0].Type != "UNPROCESSABLE" {
			t.Errorf("%s should report UNPROCESSABLE, got %s", c.name, got)
		}
	}
}
