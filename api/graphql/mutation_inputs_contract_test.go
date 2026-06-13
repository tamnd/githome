package graphql_test

import (
	"encoding/json"
	"testing"
)

// typeShapeQuery introspects one named type's input fields (with default
// values) and output fields, enough to assert the R02-24 shape fixes without
// diffing a full introspection dump.
const typeShapeQuery = `query Shape($name: String!) {
  __type(name: $name) {
    inputFields { name defaultValue }
    fields { name }
  }
}`

type typeShape struct {
	inputs  map[string]string
	outputs map[string]bool
}

func introspectType(t *testing.T, name string, srvPost func(query string, vars map[string]any) []byte) typeShape {
	t.Helper()
	got := srvPost(typeShapeQuery, map[string]any{"name": name})
	var env struct {
		Data struct {
			Type *struct {
				InputFields []struct {
					Name         string  `json:"name"`
					DefaultValue *string `json:"defaultValue"`
				} `json:"inputFields"`
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"__type"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal %s shape: %v\n%s", name, err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("introspecting %s returned errors: %v", name, env.Errors)
	}
	if env.Data.Type == nil {
		t.Fatalf("schema has no type named %s", name)
	}
	shape := typeShape{inputs: map[string]string{}, outputs: map[string]bool{}}
	for _, f := range env.Data.Type.InputFields {
		v := ""
		if f.DefaultValue != nil {
			v = *f.DefaultValue
		}
		shape.inputs[f.Name] = v
	}
	for _, f := range env.Data.Type.Fields {
		shape.outputs[f.Name] = true
	}
	return shape
}

// TestMutationInputShapes confirms R02-24: the mutation inputs and payloads
// carry the fields and default values gh's documents send and read.
func TestMutationInputShapes(t *testing.T) {
	srv, token := graphqlServer(t)
	srvPost := func(query string, vars map[string]any) []byte {
		return post(t, srv, token, query, vars)
	}

	// CreateIssueInput accepts the metadata gh issue create sends, including the
	// project and template fields Githome accepts for compatibility.
	createInput := introspectType(t, "CreateIssueInput", srvPost)
	for _, f := range []string{"assigneeIds", "labelIds", "milestoneId", "projectIds", "issueTemplate"} {
		if _, ok := createInput.inputs[f]; !ok {
			t.Errorf("CreateIssueInput is missing the %s field", f)
		}
	}

	// AddPullRequestReviewInput carries gh's modern line-anchored threads input.
	reviewInput := introspectType(t, "AddPullRequestReviewInput", srvPost)
	if _, ok := reviewInput.inputs["threads"]; !ok {
		t.Errorf("AddPullRequestReviewInput is missing the threads field")
	}

	// MergePullRequestInput defaults mergeMethod to MERGE, matching GitHub, so a
	// document that omits it validates and merges with a merge commit.
	mergeInput := introspectType(t, "MergePullRequestInput", srvPost)
	if dv, ok := mergeInput.inputs["mergeMethod"]; !ok {
		t.Errorf("MergePullRequestInput is missing mergeMethod")
	} else if dv != "MERGE" {
		t.Errorf("MergePullRequestInput.mergeMethod defaultValue = %q, want MERGE", dv)
	}

	// AddCommentPayload exposes the subject and timeline edge GitHub returns.
	addComment := introspectType(t, "AddCommentPayload", srvPost)
	for _, f := range []string{"subject", "commentEdge", "timelineEdge"} {
		if !addComment.outputs[f] {
			t.Errorf("AddCommentPayload is missing the %s field", f)
		}
	}

	// RequestReviewsPayload exposes the requested-reviewer edge.
	requestReviews := introspectType(t, "RequestReviewsPayload", srvPost)
	if !requestReviews.outputs["requestedReviewersEdge"] {
		t.Errorf("RequestReviewsPayload is missing requestedReviewersEdge")
	}

	// DraftPullRequestReviewThread is a real input type, not a dangling
	// reference, so a document declaring it validates.
	thread := introspectType(t, "DraftPullRequestReviewThread", srvPost)
	for _, f := range []string{"path", "line", "body"} {
		if _, ok := thread.inputs[f]; !ok {
			t.Errorf("DraftPullRequestReviewThread is missing the %s field", f)
		}
	}
}

// addCommentWithSubjectQuery posts an addComment mutation reading back the new
// fields: the subject as an Issue and the comment seen through the timeline
// edge. It proves the payload resolvers fill them, not just that the schema
// declares them.
const addCommentWithSubjectQuery = `mutation AddComment($id: ID!, $body: String!) {
  addComment(input: {subjectId: $id, body: $body}) {
    subject { ... on Issue { number } }
    commentEdge { node { body } }
    timelineEdge { cursor node { body } }
  }
}`

// TestAddCommentSubjectAndTimeline confirms the AddCommentPayload subject and
// timelineEdge resolve to the commented issue and the new comment.
func TestAddCommentSubjectAndTimeline(t *testing.T) {
	srv, token := issueServer(t)
	id := issueNodeID(t, srv, token, 2)
	got := post(t, srv, token, addCommentWithSubjectQuery, map[string]any{"id": id, "body": "a fresh comment"})

	var env struct {
		Data struct {
			AddComment struct {
				Subject struct {
					Number int `json:"number"`
				} `json:"subject"`
				CommentEdge struct {
					Node struct {
						Body string `json:"body"`
					} `json:"node"`
				} `json:"commentEdge"`
				TimelineEdge struct {
					Cursor string `json:"cursor"`
					Node   struct {
						Body string `json:"body"`
					} `json:"node"`
				} `json:"timelineEdge"`
			} `json:"addComment"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("addComment returned errors: %v\nbody %s", env.Errors, got)
	}
	ac := env.Data.AddComment
	if ac.Subject.Number != 2 {
		t.Errorf("subject issue number = %d, want 2", ac.Subject.Number)
	}
	if ac.CommentEdge.Node.Body != "a fresh comment" {
		t.Errorf("commentEdge body = %q, want the posted body", ac.CommentEdge.Node.Body)
	}
	if ac.TimelineEdge.Node.Body != "a fresh comment" {
		t.Errorf("timelineEdge body = %q, want the posted body", ac.TimelineEdge.Node.Body)
	}
}
