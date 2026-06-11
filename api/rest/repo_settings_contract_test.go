package rest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// decodeObject unmarshals a JSON object response for field-level assertions.
func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, body)
	}
	return m
}

// firstFieldError digs errors[0] out of a 422 body.
func firstFieldError(t *testing.T, body []byte) map[string]any {
	t.Helper()
	m := decodeObject(t, body)
	errs, ok := m["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("422 body has no errors array: %s", body)
	}
	fe, ok := errs[0].(map[string]any)
	if !ok {
		t.Fatalf("errors[0] is not an object: %s", body)
	}
	return fe
}

func TestRepoCreateMissingNameStructured(t *testing.T) {
	fx := repoServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/user/repos", fx.token, `{"description":"nameless"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", resp.StatusCode, body)
	}
	m := decodeObject(t, body)
	if m["message"] != "Repository creation failed." {
		t.Errorf("message = %v, want %q", m["message"], "Repository creation failed.")
	}
	fe := firstFieldError(t, body)
	if fe["resource"] != "Repository" || fe["field"] != "name" || fe["code"] != "custom" {
		t.Errorf("errors[0] = %v, want resource=Repository field=name code=custom", fe)
	}
}

func TestRepoCreateUnknownTemplates(t *testing.T) {
	fx := repoServer(t)
	for field, payload := range map[string]string{
		"gitignore_template": `{"name":"bad-ignore","gitignore_template":"NotALanguage"}`,
		"license_template":   `{"name":"bad-license","license_template":"wtfpl"}`,
	} {
		resp, body := authedSend(t, fx.srv, http.MethodPost, "/user/repos", fx.token, payload)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d, want 422; body %s", field, resp.StatusCode, body)
		}
		fe := firstFieldError(t, body)
		if fe["field"] != field || fe["code"] != "invalid" {
			t.Errorf("%s: errors[0] = %v, want field=%s code=invalid", field, fe, field)
		}
	}
}

func TestRepoCreateSettingsAndAutoInit(t *testing.T) {
	fx := repoServer(t)
	payload := `{
		"name": "seeded",
		"description": "a seeded repo",
		"auto_init": true,
		"gitignore_template": "Go",
		"license_template": "mit",
		"has_issues": false,
		"has_wiki": false,
		"is_template": true,
		"allow_squash_merge": false,
		"team_id": 7
	}`
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/user/repos", fx.token, payload)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201; body %s", resp.StatusCode, body)
	}
	m := decodeObject(t, body)
	if m["has_issues"] != false {
		t.Errorf("has_issues = %v, want false", m["has_issues"])
	}
	if m["has_wiki"] != false {
		t.Errorf("has_wiki = %v, want false", m["has_wiki"])
	}
	if m["is_template"] != true {
		t.Errorf("is_template = %v, want true", m["is_template"])
	}
	if m["pushed_at"] == nil {
		t.Errorf("pushed_at = nil, want the auto-init commit time")
	}

	// The initial commit must carry the README and both templates.
	for path, want := range map[string]string{
		"README.md":  "# seeded",
		".gitignore": "go.work",
		"LICENSE":    "MIT License",
	} {
		resp, body := authedGet(t, fx.srv, "/repos/octocat/seeded/contents/"+path, "token "+fx.token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("contents %s: status %d, body %s", path, resp.StatusCode, body)
		}
		cm := decodeObject(t, body)
		enc, _ := cm["content"].(string)
		raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(enc, "\n", ""))
		if err != nil {
			t.Fatalf("contents %s: decode: %v", path, err)
		}
		if !strings.Contains(string(raw), want) {
			t.Errorf("contents %s: missing %q in:\n%s", path, want, raw)
		}
	}

	// The license body substitutes the year and holder placeholders.
	resp, body = authedGet(t, fx.srv, "/repos/octocat/seeded/contents/LICENSE", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("contents LICENSE: status %d", resp.StatusCode)
	}
	cm := decodeObject(t, body)
	enc, _ := cm["content"].(string)
	raw, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(enc, "\n", ""))
	if strings.Contains(string(raw), "[year]") || strings.Contains(string(raw), "[fullname]") {
		t.Errorf("LICENSE still has placeholders:\n%s", raw)
	}
}

func TestRepoCreateTemplateImpliesInit(t *testing.T) {
	fx := repoServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/user/repos", fx.token,
		`{"name":"lic-only","license_template":"bsd-3-clause"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201; body %s", resp.StatusCode, body)
	}
	resp, _ = authedGet(t, fx.srv, "/repos/octocat/lic-only/contents/LICENSE", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LICENSE: status %d, want 200", resp.StatusCode)
	}
}

func TestRepoPatchMergeSettingsAccepted(t *testing.T) {
	fx := repoServer(t)
	payload := `{
		"allow_squash_merge": false,
		"allow_merge_commit": false,
		"delete_branch_on_merge": true,
		"allow_update_branch": true,
		"web_commit_signoff_required": true,
		"security_and_analysis": {"secret_scanning": {"status": "enabled"}}
	}`
	resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello", fx.token, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", resp.StatusCode, body)
	}
	m := decodeObject(t, body)
	if m["name"] != "hello" {
		t.Errorf("name = %v, want hello", m["name"])
	}
}

func TestRepoPatchVisibility(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello", fx.token,
		`{"visibility":"private"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", resp.StatusCode, body)
	}
	m := decodeObject(t, body)
	if m["private"] != true {
		t.Errorf("private = %v, want true", m["private"])
	}
	if m["visibility"] != "private" {
		t.Errorf("visibility = %v, want private", m["visibility"])
	}

	resp, body = authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello", fx.token,
		`{"visibility":"internal"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", resp.StatusCode, body)
	}
	fe := firstFieldError(t, body)
	if fe["field"] != "visibility" || fe["code"] != "invalid" {
		t.Errorf("errors[0] = %v, want field=visibility code=invalid", fe)
	}
}
