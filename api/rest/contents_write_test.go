package rest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// jsonStrIn reads body[outer][key] as a string.
func jsonStrIn(t *testing.T, body []byte, outer, key string) string {
	t.Helper()
	var m map[string]map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	s, _ := m[outer][key].(string)
	if s == "" {
		t.Fatalf("%s.%s missing in %s", outer, key, body)
	}
	return s
}

// TestContentsPutShaSemantics covers GitHub's compare-and-swap contract on
// PUT /repos/{owner}/{repo}/contents/{path}: a create is 201, updating an
// existing file without its sha is 422, a stale sha is 409, and an update with
// the right sha is 200.
func TestContentsPutShaSemantics(t *testing.T) {
	fx := repoServer(t)
	b64 := base64.StdEncoding.EncodeToString([]byte("fresh body\n"))

	// Create a new file: no sha needed, 201.
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/new.txt", fx.token,
		`{"message":"add new.txt","content":"`+b64+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, want 201, body %s", resp.StatusCode, body)
	}

	// Update an existing file without sha: GitHub refuses with 422.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/README.md", fx.token,
		`{"message":"clobber","content":"`+b64+`"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("no-sha update status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `Invalid request.\n\n\"sha\" wasn't supplied.`) {
		t.Errorf("no-sha update message: %s", body)
	}

	// Update with a stale sha: 409 naming the path and the supplied sha.
	stale := "0000000000000000000000000000000000000000"
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/README.md", fx.token,
		`{"message":"stale","content":"`+b64+`","sha":"`+stale+`"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale-sha update status %d, want 409, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"message":"README.md does not match `+stale+`"`) {
		t.Errorf("stale-sha update message: %s", body)
	}

	// Update with the current sha: 200, not 201.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/README.md", fx.token,
		`{"message":"update readme","content":"`+b64+`","sha":"`+fx.blobSHA+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"path":"README.md"`) {
		t.Errorf("update body: %s", body)
	}
}

// TestContentsPutResponseShape pins the PUT response to GitHub's full shape:
// the content object carries the urls and _links of a contents GET, and the
// commit is the full git commit with tree, parents, and verification.
func TestContentsPutResponseShape(t *testing.T) {
	fx := repoServer(t)
	b64 := base64.StdEncoding.EncodeToString([]byte("shape body\n"))
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/docs/shape.txt", fx.token,
		`{"message":"add shape","content":"`+b64+`","committer":{"name":"Octo Cat","email":"octo@test.internal"}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"name":"shape.txt"`, `"path":"docs/shape.txt"`, `"size":11`, `"type":"file"`,
		`"html_url"`, `"git_url"`, `"download_url"`, `"_links"`,
		`"tree":{`, `"parents":[`, `"verification":{`, `"node_id"`,
		`"name":"Octo Cat"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("put response missing %s: %s", want, body)
		}
	}

	// DELETE answers the same full commit beside content: null.
	sha := jsonStrIn(t, body, "content", "sha")
	resp, body = authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/contents/docs/shape.txt", fx.token,
		`{"message":"drop shape","sha":"`+sha+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"content":null`, `"tree":{`, `"verification":{`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("delete response missing %s: %s", want, body)
		}
	}
}

// TestContentsDeleteShaSemantics covers DELETE: sha is required (422 without
// it), must match (409 when stale), and the success body carries content: null.
func TestContentsDeleteShaSemantics(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/contents/README.md", fx.token,
		`{"message":"drop readme"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("no-sha delete status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `Invalid request.\n\n\"sha\" wasn't supplied.`) {
		t.Errorf("no-sha delete message: %s", body)
	}

	stale := "1111111111111111111111111111111111111111"
	resp, body = authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/contents/README.md", fx.token,
		`{"message":"drop readme","sha":"`+stale+`"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale-sha delete status %d, want 409, body %s", resp.StatusCode, body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/contents/README.md", fx.token,
		`{"message":"drop readme","sha":"`+fx.blobSHA+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"content":null`) {
		t.Errorf("delete body missing content null: %s", body)
	}

	// The file is gone now; deleting again is 404.
	resp, _ = authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/contents/README.md", fx.token,
		`{"message":"drop readme","sha":"`+fx.blobSHA+`"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status %d, want 404", resp.StatusCode)
	}
}
