package graphql_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestCommitDataFields covers R02-1A: a Commit narrowed from Ref.target serves
// committedDate, authoredDate, abbreviatedOid, tree, parents, and authors off
// the underlying git commit, the way gh pr view --json commits reads them.
func TestCommitDataFields(t *testing.T) {
	srv, token := graphqlServer(t)
	q := `query($owner: String!, $name: String!) {
	  repository(owner: $owner, name: $name) {
	    defaultBranchRef {
	      target {
	        ... on Commit {
	          oid
	          abbreviatedOid
	          committedDate
	          authoredDate
	          tree { oid }
	          parents(first: 10) { totalCount nodes { oid } }
	          authors(first: 10) { totalCount nodes { name email } }
	        }
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
						Oid            string `json:"oid"`
						AbbreviatedOid string `json:"abbreviatedOid"`
						CommittedDate  string `json:"committedDate"`
						AuthoredDate   string `json:"authoredDate"`
						Tree           struct {
							Oid string `json:"oid"`
						} `json:"tree"`
						Parents struct {
							TotalCount int `json:"totalCount"`
							Nodes      []struct {
								Oid string `json:"oid"`
							} `json:"nodes"`
						} `json:"parents"`
						Authors struct {
							TotalCount int `json:"totalCount"`
							Nodes      []struct {
								Name  string `json:"name"`
								Email string `json:"email"`
							} `json:"nodes"`
						} `json:"authors"`
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
	c := env.Data.Repository.DefaultBranchRef.Target

	if _, err := time.Parse(time.RFC3339, c.CommittedDate); err != nil {
		t.Errorf("committedDate = %q, want an RFC3339 instant: %v", c.CommittedDate, err)
	}
	if _, err := time.Parse(time.RFC3339, c.AuthoredDate); err != nil {
		t.Errorf("authoredDate = %q, want an RFC3339 instant: %v", c.AuthoredDate, err)
	}
	if len(c.Tree.Oid) != 40 {
		t.Errorf("tree.oid = %q, want a full tree SHA", c.Tree.Oid)
	}
	if c.Tree.Oid == c.Oid {
		t.Errorf("tree.oid = commit oid %q, want the root tree, not the commit", c.Oid)
	}
	// The fixture's default branch is a single root commit, so it has no parents.
	if c.Parents.TotalCount != 0 || len(c.Parents.Nodes) != 0 {
		t.Errorf("parents = %+v, want none on the root commit", c.Parents)
	}
	if c.Authors.TotalCount != 1 || len(c.Authors.Nodes) != 1 {
		t.Fatalf("authors = %+v, want exactly the commit's author", c.Authors)
	}
	if c.Authors.Nodes[0].Name == "" || !strings.Contains(c.Authors.Nodes[0].Email, "@") {
		t.Errorf("author = %+v, want a name and an email", c.Authors.Nodes[0])
	}
}

// TestStatusStateEnumOrder covers R02-1A: the StatusState and CheckStatusState
// enum values are declared in GitHub's published order, so a client that indexes
// the enum by position sees the same value at each ordinal GitHub does.
func TestStatusStateEnumOrder(t *testing.T) {
	srv, token := graphqlServer(t)
	q := `query($n: String!) { __type(name: $n) { enumValues { name } } }`

	cases := []struct {
		typeName string
		want     []string
	}{
		{"StatusState", []string{"EXPECTED", "ERROR", "FAILURE", "PENDING", "SUCCESS"}},
		{"CheckStatusState", []string{"REQUESTED", "QUEUED", "IN_PROGRESS", "COMPLETED", "WAITING", "PENDING"}},
	}
	for _, tc := range cases {
		got := post(t, srv, token, q, map[string]any{"n": tc.typeName})
		var env struct {
			Data struct {
				Type struct {
					EnumValues []struct {
						Name string `json:"name"`
					} `json:"enumValues"`
				} `json:"__type"`
			} `json:"data"`
		}
		if err := json.Unmarshal(got, &env); err != nil {
			t.Fatalf("%s unmarshal: %v, body %s", tc.typeName, err, got)
		}
		names := make([]string, len(env.Data.Type.EnumValues))
		for i, v := range env.Data.Type.EnumValues {
			names[i] = v.Name
		}
		if strings.Join(names, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s order = %v, want GitHub order %v", tc.typeName, names, tc.want)
		}
	}
}
