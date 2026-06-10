package rest

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAPIRootContract covers GET on the API root: the hypermedia *_url template
// document, served at the prefixed root with and without a trailing slash and at
// the bare github.com-style root.
func TestAPIRootContract(t *testing.T) {
	fx := repoServer(t)

	for _, path := range []string{"/api/v3/", "/api/v3", "/"} {
		resp, body := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", path, resp.StatusCode, body)
		}
		var doc map[string]string
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("%s: unmarshal: %v", path, err)
		}
		want := map[string]string{
			"current_user_url":                     "https://git.test.internal/api/v3/user",
			"current_user_authorizations_html_url": "https://git.test.internal/settings/connections/applications{/client_id}",
			"code_search_url":                      "https://git.test.internal/api/v3/search/code?q={query}{&page,per_page,sort,order}",
			"emojis_url":                           "https://git.test.internal/api/v3/emojis",
			"following_url":                        "https://git.test.internal/api/v3/user/following{/target}",
			"gists_url":                            "https://git.test.internal/api/v3/gists{/gist_id}",
			"label_search_url":                     "https://git.test.internal/api/v3/search/labels?q={query}&repository_id={repository_id}{&page,per_page}",
			"notifications_url":                    "https://git.test.internal/api/v3/notifications",
			"organization_repositories_url":        "https://git.test.internal/api/v3/orgs/{org}/repos{?type,page,per_page,sort}",
			"rate_limit_url":                       "https://git.test.internal/api/v3/rate_limit",
			"repository_url":                       "https://git.test.internal/api/v3/repos/{owner}/{repo}",
			"current_user_repositories_url":        "https://git.test.internal/api/v3/user/repos{?type,page,per_page,sort}",
			"starred_url":                          "https://git.test.internal/api/v3/user/starred{/owner}{/repo}",
			"user_url":                             "https://git.test.internal/api/v3/users/{user}",
			"user_search_url":                      "https://git.test.internal/api/v3/search/users?q={query}{&page,per_page,sort,order}",
		}
		for key, value := range want {
			if doc[key] != value {
				t.Errorf("%s: %s = %q, want %q", path, key, doc[key], value)
			}
		}
		if len(doc) != 32 {
			t.Errorf("%s: %d fields, want 32", path, len(doc))
		}
	}
}
