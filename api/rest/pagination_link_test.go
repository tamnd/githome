package rest

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestTagsLinkHeader pins the exact Link header bytes on a counted in-memory
// list. The fixture has two tags, so per_page=1 yields two pages with GitHub's
// rel order: prev, next, last, first.
func TestTagsLinkHeader(t *testing.T) {
	fx := repoServer(t)

	resp, body := get(t, fx.srv, "/repos/octocat/hello/tags?per_page=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	want := `<https://git.test.internal/repos/octocat/hello/tags?page=2&per_page=1>; rel="next", ` +
		`<https://git.test.internal/repos/octocat/hello/tags?page=2&per_page=1>; rel="last"`
	if got := resp.Header.Get("Link"); got != want {
		t.Errorf("page 1 Link:\n got %q\nwant %q", got, want)
	}

	resp, body = get(t, fx.srv, "/repos/octocat/hello/tags?page=2&per_page=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 2 status %d, body %s", resp.StatusCode, body)
	}
	want = `<https://git.test.internal/repos/octocat/hello/tags?page=1&per_page=1>; rel="prev", ` +
		`<https://git.test.internal/repos/octocat/hello/tags?page=1&per_page=1>; rel="first"`
	if got := resp.Header.Get("Link"); got != want {
		t.Errorf("page 2 Link:\n got %q\nwant %q", got, want)
	}

	// The whole list on one page carries no header at all, like GitHub.
	resp, _ = get(t, fx.srv, "/repos/octocat/hello/tags")
	if got := resp.Header.Get("Link"); got != "" {
		t.Errorf("single page Link = %q, want none", got)
	}
}

// TestCommitsLinkHeader pins the uncounted Link header on the commit walk:
// rel="next" appears only while more history exists and rel="last" never does,
// because the endpoint does not count the walk.
func TestCommitsLinkHeader(t *testing.T) {
	fx := repoServer(t)

	resp, body := get(t, fx.srv, "/repos/octocat/hello/commits?per_page=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	want := `<https://git.test.internal/repos/octocat/hello/commits?page=2&per_page=1>; rel="next"`
	if got := resp.Header.Get("Link"); got != want {
		t.Errorf("page 1 Link:\n got %q\nwant %q", got, want)
	}

	resp, body = get(t, fx.srv, "/repos/octocat/hello/commits?page=2&per_page=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 2 status %d, body %s", resp.StatusCode, body)
	}
	want = `<https://git.test.internal/repos/octocat/hello/commits?page=1&per_page=1>; rel="prev", ` +
		`<https://git.test.internal/repos/octocat/hello/commits?page=1&per_page=1>; rel="first"`
	if got := resp.Header.Get("Link"); got != want {
		t.Errorf("page 2 Link:\n got %q\nwant %q", got, want)
	}
}

// TestRepoListPagination covers R01-18: the repo lists honor page/per_page and
// carry the counted Link header.
func TestRepoListPagination(t *testing.T) {
	fx := repoServer(t)

	// A second repo so per_page=1 splits the list.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/user/repos", fx.token, `{"name":"world"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create repo status %d, body %s", resp.StatusCode, body)
	}

	for _, path := range []string{"/users/octocat/repos", "/user/repos"} {
		resp, body := authedGet(t, fx.srv, path+"?per_page=1", "token "+fx.token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", path, resp.StatusCode, body)
		}
		want := `<https://git.test.internal` + path + `?page=2&per_page=1>; rel="next", ` +
			`<https://git.test.internal` + path + `?page=2&per_page=1>; rel="last"`
		if got := resp.Header.Get("Link"); got != want {
			t.Errorf("%s Link:\n got %q\nwant %q", path, got, want)
		}

		// Page 2 holds the remaining repo and only that one.
		resp, body = authedGet(t, fx.srv, path+"?page=2&per_page=1", "token "+fx.token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s page 2: status %d, body %s", path, resp.StatusCode, body)
		}
		var names []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &names); err != nil {
			t.Fatalf("%s page 2: unmarshal: %v", path, err)
		}
		if len(names) != 1 {
			t.Errorf("%s page 2: %d repos, want 1", path, len(names))
		}

		// A page past the end is empty, not an error.
		resp, body = authedGet(t, fx.srv, path+"?page=9&per_page=1", "token "+fx.token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s page 9: status %d, body %s", path, resp.StatusCode, body)
		}
		if string(body) != "[]\n" && string(body) != "[]" {
			t.Errorf("%s page 9 body = %q, want empty array", path, body)
		}
	}

	// A bad page value is GitHub's 422.
	resp, _ = authedGet(t, fx.srv, "/users/octocat/repos?page=0", "token "+fx.token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("page=0 status %d, want 422", resp.StatusCode)
	}
}
