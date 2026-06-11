package graphql_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRefTargetNarrowsToCommit confirms Ref.target is the GitObject interface
// GitHub serves: the concrete type is Commit, an inline fragment narrows to
// it, and the commit carries a node id and the abbreviated oid.
func TestRefTargetNarrowsToCommit(t *testing.T) {
	srv, token := graphqlServer(t)
	q := `query($owner: String!, $name: String!) {
	  repository(owner: $owner, name: $name) {
	    defaultBranchRef {
	      target {
	        __typename
	        id
	        oid
	        abbreviatedOid
	        ... on Commit { message }
	      }
	    }
	  }
	}`
	got := post(t, srv, token, q, map[string]any{"owner": "octocat", "name": "hello"})
	var env struct {
		Data struct {
			Repository struct {
				DefaultBranchRef struct {
					Target struct {
						Typename       string `json:"__typename"`
						ID             string `json:"id"`
						Oid            string `json:"oid"`
						AbbreviatedOid string `json:"abbreviatedOid"`
						Message        string `json:"message"`
					} `json:"target"`
				} `json:"defaultBranchRef"`
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
	target := env.Data.Repository.DefaultBranchRef.Target
	if target.Typename != "Commit" {
		t.Errorf("target __typename = %q, want Commit", target.Typename)
	}
	if !strings.HasPrefix(target.ID, "C_") {
		t.Errorf("target id = %q, want a C_ commit node id", target.ID)
	}
	if len(target.Oid) != 40 {
		t.Errorf("target oid = %q, want a full SHA", target.Oid)
	}
	if target.AbbreviatedOid != target.Oid[:7] {
		t.Errorf("abbreviatedOid = %q, want %q", target.AbbreviatedOid, target.Oid[:7])
	}
	if target.Message != "initial commit" {
		t.Errorf("message = %q, want the fragment-narrowed commit message", target.Message)
	}

	// The commit node id resolves back through node().
	nodeQ := `query($id: ID!) { node(id: $id) { __typename ... on Commit { oid } } }`
	got = post(t, srv, token, nodeQ, map[string]any{"id": target.ID})
	var nodeEnv struct {
		Data struct {
			Node struct {
				Typename string `json:"__typename"`
				Oid      string `json:"oid"`
			} `json:"node"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &nodeEnv); err != nil {
		t.Fatalf("unmarshal node: %v, body %s", err, got)
	}
	if len(nodeEnv.Errors) != 0 {
		t.Fatalf("node errors = %v, body %s", nodeEnv.Errors, got)
	}
	if nodeEnv.Data.Node.Typename != "Commit" || nodeEnv.Data.Node.Oid != target.Oid {
		t.Errorf("node(%q) = %+v, want the same commit back", target.ID, nodeEnv.Data.Node)
	}
}
