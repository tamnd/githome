package rest

import (
	"encoding/json"
	"net/http"
	"testing"
)

// gistFiles decodes the files map out of a gist response body.
func gistFiles(t *testing.T, body []byte) map[string]struct {
	Content string `json:"content"`
} {
	t.Helper()
	var g struct {
		Files map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode gist: %v from %s", err, body)
	}
	return g.Files
}

// TestGistUpdateRenameAndDelete covers the GitHub PATCH /gists/{id} file
// semantics the compat review flagged: {"old":{"filename":"new"}} renames a
// file keeping its content, {"x":null} deletes it, and a rename combined
// with content replaces the body under the new name.
func TestGistUpdateRenameAndDelete(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/gists", fx.token,
		`{"files":{"a.txt":{"content":"alpha"},"b.txt":{"content":"beta"}},"public":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed gist: status %d, body %s", resp.StatusCode, body)
	}
	var gist struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &gist); err != nil || gist.ID == "" {
		t.Fatalf("decode gist id: %v from %s", err, body)
	}
	path := "/gists/" + gist.ID

	// Rename only: content travels to the new name.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, path, fx.token,
		`{"files":{"a.txt":{"filename":"renamed.txt"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: status %d, body %s", resp.StatusCode, body)
	}
	files := gistFiles(t, body)
	if _, ok := files["a.txt"]; ok {
		t.Errorf("a.txt still present after rename: %s", body)
	}
	if f, ok := files["renamed.txt"]; !ok {
		t.Errorf("renamed.txt missing after rename: %s", body)
	} else if f.Content != "alpha" {
		t.Errorf("renamed.txt content = %q, want alpha", f.Content)
	}
	if _, ok := files["b.txt"]; !ok {
		t.Errorf("b.txt should be untouched by the rename: %s", body)
	}

	// Rename plus content: new name, new body.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, path, fx.token,
		`{"files":{"renamed.txt":{"filename":"c.md","content":"gamma"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename+content: status %d, body %s", resp.StatusCode, body)
	}
	files = gistFiles(t, body)
	if _, ok := files["renamed.txt"]; ok {
		t.Errorf("renamed.txt still present after second rename: %s", body)
	}
	if f, ok := files["c.md"]; !ok {
		t.Errorf("c.md missing: %s", body)
	} else if f.Content != "gamma" {
		t.Errorf("c.md content = %q, want gamma", f.Content)
	}

	// Null entry deletes the file.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, path, fx.token,
		`{"files":{"b.txt":null}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: status %d, body %s", resp.StatusCode, body)
	}
	files = gistFiles(t, body)
	if _, ok := files["b.txt"]; ok {
		t.Errorf("b.txt still present after null delete: %s", body)
	}
	if len(files) != 1 {
		t.Errorf("want exactly c.md left, got %d files: %s", len(files), body)
	}

	// Plain content update still works.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, path, fx.token,
		`{"files":{"c.md":{"content":"updated"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("content update: status %d, body %s", resp.StatusCode, body)
	}
	if f := gistFiles(t, body)["c.md"]; f.Content != "updated" {
		t.Errorf("c.md content = %q, want updated", f.Content)
	}

	// Renaming a file that does not exist is a validation error.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, path, fx.token,
		`{"files":{"nope.txt":{"filename":"x.txt"}}}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("rename missing file: status %d, body %s", resp.StatusCode, body)
	}
}
