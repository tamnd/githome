package graphql_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// postWithHeader posts a GraphQL document like post, plus one extra header.
func postWithHeader(t *testing.T, srv *httptest.Server, token, header, value, query string, vars map[string]any) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+token)
	if header != "" {
		req.Header.Set(header, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return out.Bytes()
}

func repositoryID(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Data struct {
			Repository struct {
				ID string `json:"id"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, body)
	}
	if env.Data.Repository.ID == "" {
		t.Fatalf("no repository id in %s", body)
	}
	return env.Data.Repository.ID
}

// TestGlobalIDHeaderSelectsFormat confirms the X-Github-Next-Global-ID header
// picks the node-id encoding per request: "0" asks for the legacy base64 ids,
// "1" for the new prefixed ids, and an absent header keeps the server default.
func TestGlobalIDHeaderSelectsFormat(t *testing.T) {
	srv, token := graphqlServer(t)
	vars := map[string]any{"owner": "octocat", "name": "hello"}
	q := `query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { id } }`

	legacy := repositoryID(t, postWithHeader(t, srv, token, "X-Github-Next-Global-ID", "0", q, vars))
	raw, err := base64.StdEncoding.DecodeString(legacy)
	if err != nil {
		t.Fatalf("legacy id %q is not standard base64: %v", legacy, err)
	}
	if !strings.Contains(string(raw), ":Repository") {
		t.Errorf("legacy id %q decodes to %q, want a :Repository body", legacy, raw)
	}

	next := repositoryID(t, postWithHeader(t, srv, token, "X-Github-Next-Global-ID", "1", q, vars))
	if !strings.HasPrefix(next, "R_") {
		t.Errorf("next id = %q, want an R_ prefix", next)
	}

	def := repositoryID(t, postWithHeader(t, srv, token, "", "", q, vars))
	if def != next {
		t.Errorf("default id = %q, want the configured new format %q", def, next)
	}
}

// TestNodesCapsIDs confirms nodes(ids:) rejects more than 100 ids per request,
// the same cap GitHub applies.
func TestNodesCapsIDs(t *testing.T) {
	srv, token := graphqlServer(t)
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "I_" + strconv.Itoa(i)
	}
	got := postWithHeader(t, srv, token, "", "", `query($ids: [ID!]!) { nodes(ids: $ids) { id } }`, map[string]any{"ids": ids})
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) == 0 {
		t.Fatalf("no errors, body %s", got)
	}
	if want := "You may not provide more than 100 node ids."; env.Errors[0].Message != want {
		t.Errorf("error message = %q, want %q", env.Errors[0].Message, want)
	}
}
