package graphql_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamnd/githome/api/graphql"
)

// gqlResponse is the minimal shape of a GraphQL response (data + errors).
type gqlResponse struct {
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func doGQL(t *testing.T, h http.Handler, query string) gqlResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp gqlResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body=%s)", err, rr.Body.String())
	}
	return resp
}

// TestNodeLimitRejection confirms the node-count gate rejects a document that
// could return more than GitHub's 500,000-node ceiling. A nested first:1000 in
// first:1000 is 1,000,000 + 1,000 nodes, well over the cap.
func TestNodeLimitRejection(t *testing.T) {
	h := graphql.NewHandler(graphql.Deps{})
	query := `{ repository(owner:"o",name:"r") { pullRequests(first:1000) { nodes { comments(first:1000) { nodes { id } } } } } }`
	resp := doGQL(t, h, query)
	if len(resp.Errors) == 0 {
		t.Fatal("expected node-limit error, got none")
	}
	msg := resp.Errors[0].Message
	if !strings.Contains(strings.ToLower(msg), "node count") &&
		!strings.Contains(strings.ToLower(msg), "exceeds") {
		t.Fatalf("unexpected error message: %q", msg)
	}
	ext := resp.Errors[0].Extensions
	if code, _ := ext["code"].(string); code != "MAX_NODE_LIMIT_EXCEEDED" {
		t.Fatalf("expected MAX_NODE_LIMIT_EXCEEDED extension code, got %q (full error: %q)", code, msg)
	}
}

// TestNodeLimitAllowsLargeButBoundedQuery confirms a query the old fixed
// 5000-point cap rejected — first:5001 on a single connection — passes the
// node-count gate, the way GitHub serves it (5,001 nodes << 500,000). It may
// still produce a resolution error for missing data, but never a node-limit one.
func TestNodeLimitAllowsLargeButBoundedQuery(t *testing.T) {
	h := graphql.NewHandler(graphql.Deps{})
	query := `{ repository(owner:"o",name:"r") { issues(first:5001) { nodes { id } } } }`
	resp := doGQL(t, h, query)
	for _, e := range resp.Errors {
		if code, _ := e.Extensions["code"].(string); code == "MAX_NODE_LIMIT_EXCEEDED" {
			t.Fatalf("node-limit error on a 5001-node query GitHub serves: %q", e.Message)
		}
	}
}

// overDepthQuery is a valid GraphQL document that nests 27 levels deep by
// chaining twenty named fragment spreads on IssueComment. Fragment spread
// pointers are resolved by the parser so selectionDepth follows them. The
// path is:
//
//	query(0) > repository(1) > issues(2) > nodes(3) > comments(4) >
//	nodes(5) > ...F1(6) > ... > ...F20(25) > author(26) > login(27)
const overDepthQuery = `
fragment F20 on IssueComment { author { login } }
fragment F19 on IssueComment { ...F20 }
fragment F18 on IssueComment { ...F19 }
fragment F17 on IssueComment { ...F18 }
fragment F16 on IssueComment { ...F17 }
fragment F15 on IssueComment { ...F16 }
fragment F14 on IssueComment { ...F15 }
fragment F13 on IssueComment { ...F14 }
fragment F12 on IssueComment { ...F13 }
fragment F11 on IssueComment { ...F12 }
fragment F10 on IssueComment { ...F11 }
fragment F9 on IssueComment { ...F10 }
fragment F8 on IssueComment { ...F9 }
fragment F7 on IssueComment { ...F8 }
fragment F6 on IssueComment { ...F7 }
fragment F5 on IssueComment { ...F6 }
fragment F4 on IssueComment { ...F5 }
fragment F3 on IssueComment { ...F4 }
fragment F2 on IssueComment { ...F3 }
fragment F1 on IssueComment { ...F2 }

{
  repository(owner:"o", name:"r") {
    issues {
      nodes {
        comments {
          nodes {
            ...F1
          }
        }
      }
    }
  }
}`

func TestDepthLimitRejection(t *testing.T) {
	h := graphql.NewHandler(graphql.Deps{})
	resp := doGQL(t, h, overDepthQuery)
	if len(resp.Errors) == 0 {
		t.Fatal("expected depth error, got none")
	}
	msg := resp.Errors[0].Message
	if !strings.Contains(strings.ToLower(msg), "depth") &&
		!strings.Contains(strings.ToLower(msg), "exceeds") {
		t.Fatalf("unexpected error message: %q", msg)
	}
	ext := resp.Errors[0].Extensions
	if code, _ := ext["code"].(string); code != "MAX_QUERY_DEPTH" {
		t.Fatalf("expected MAX_QUERY_DEPTH extension code, got %q (full error: %q)", code, msg)
	}
}

func TestDepthWithinLimitPasses(t *testing.T) {
	// A shallow query well under the 25-level limit should not produce a
	// depth error (it may produce a resolution error since there is no data,
	// but not a depth error).
	h := graphql.NewHandler(graphql.Deps{})
	query := `{ repository(owner:"o",name:"r") { issues { nodes { id title } } } }`
	resp := doGQL(t, h, query)
	for _, e := range resp.Errors {
		if code, _ := e.Extensions["code"].(string); code == "MAX_QUERY_DEPTH" {
			t.Fatalf("depth error on a shallow query: %q", e.Message)
		}
	}
}
