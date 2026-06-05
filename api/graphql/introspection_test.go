package graphql_test

import (
	"encoding/json"
	"testing"
)

// introspectionQuery asks the schema to describe itself: the query root's name,
// every type's name and kind, and for each field its name and the names of its
// arguments. It is the slice of the standard GraphQL IntrospectionQuery the
// parity assertions below read, kept small so the test states exactly what it
// depends on rather than diffing a full introspection dump that churns whenever
// a field is added.
const introspectionQuery = `query Introspection {
  __schema {
    queryType { name }
    mutationType { name }
    types {
      kind
      name
      fields(includeDeprecated: true) {
        name
        args { name }
      }
    }
  }
}`

// introspected is the shape introspectionQuery returns, decoded just far enough
// to walk the type and field tree.
type introspected struct {
	Data struct {
		Schema struct {
			QueryType    struct{ Name string } `json:"queryType"`
			MutationType struct{ Name string } `json:"mutationType"`
			Types        []struct {
				Kind   string `json:"kind"`
				Name   string `json:"name"`
				Fields []struct {
					Name string `json:"name"`
					Args []struct {
						Name string `json:"name"`
					} `json:"args"`
				} `json:"fields"`
			} `json:"types"`
		} `json:"__schema"`
	} `json:"data"`
	Errors []any `json:"errors"`
}

// TestIntrospectionParity confirms the served schema is introspectable and
// exposes the type and connection shape gh and other Relay clients rely on: the
// roots are named Query and Mutation, the GitHub-named object types are present,
// PageInfo carries the four Relay fields, and the top-level issue and pull
// request connections accept forward and backward pagination arguments.
func TestIntrospectionParity(t *testing.T) {
	srv, token := graphqlServer(t)
	got := post(t, srv, token, introspectionQuery, nil)

	var res introspected
	if err := json.Unmarshal(got, &res); err != nil {
		t.Fatalf("unmarshal introspection: %v\n%s", err, got)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("introspection returned errors: %v", res.Errors)
	}

	if res.Data.Schema.QueryType.Name != "Query" {
		t.Errorf("query root = %q, want Query", res.Data.Schema.QueryType.Name)
	}
	if res.Data.Schema.MutationType.Name != "Mutation" {
		t.Errorf("mutation root = %q, want Mutation", res.Data.Schema.MutationType.Name)
	}

	types := make(map[string]struct {
		kind   string
		fields map[string]map[string]bool
	})
	for _, ty := range res.Data.Schema.Types {
		fields := make(map[string]map[string]bool, len(ty.Fields))
		for _, f := range ty.Fields {
			args := make(map[string]bool, len(f.Args))
			for _, a := range f.Args {
				args[a.Name] = true
			}
			fields[f.Name] = args
		}
		types[ty.Name] = struct {
			kind   string
			fields map[string]map[string]bool
		}{kind: ty.Kind, fields: fields}
	}

	// The GitHub-named object types gh and other Relay clients select against.
	for _, name := range []string{
		"Query", "Mutation", "Repository", "Issue", "PullRequest",
		"PageInfo", "IssueConnection", "PullRequestConnection",
		"Label", "IssueComment", "Ref", "GitObject", "Actor",
	} {
		if _, ok := types[name]; !ok {
			t.Errorf("schema is missing the %s type", name)
		}
	}

	// PageInfo carries the four Relay pagination fields, the contract every
	// connection's pageInfo promises.
	pageInfo, ok := types["PageInfo"]
	if !ok {
		t.Fatal("PageInfo type absent")
	}
	for _, f := range []string{"hasNextPage", "hasPreviousPage", "startCursor", "endCursor"} {
		if _, ok := pageInfo.fields[f]; !ok {
			t.Errorf("PageInfo is missing the %s field", f)
		}
	}

	// The top-level connections accept both forward (first/after) and backward
	// (last/before) pagination, so a client can page from either end.
	repo, ok := types["Repository"]
	if !ok {
		t.Fatal("Repository type absent")
	}
	for _, conn := range []string{"issues", "pullRequests"} {
		args, ok := repo.fields[conn]
		if !ok {
			t.Errorf("Repository is missing the %s connection", conn)
			continue
		}
		for _, a := range []string{"first", "after", "last", "before"} {
			if !args[a] {
				t.Errorf("Repository.%s is missing the %s argument", conn, a)
			}
		}
	}
}
