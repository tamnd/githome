package rest

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestReleaseAssetOctetStreamAccept pins the R01-3G fix: octokit sends the
// octet-stream Accept with a quality parameter and alongside other media types,
// so the asset download must still serve the raw bytes rather than the JSON
// metadata.
func TestReleaseAssetOctetStreamAccept(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases", fx.token,
		`{"tag_name":"v1.0.0"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("release create status %d, body %s", resp.StatusCode, body)
	}
	releaseID := jsonInt(t, body, "id")

	resp, body = authedSend(t, fx.srv, http.MethodPost,
		"/api/uploads/repos/octocat/hello/releases/"+itoa(releaseID)+"/assets?name=bin.tar.gz", fx.token,
		"binary bytes")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d, body %s", resp.StatusCode, body)
	}
	assetID := jsonInt(t, body, "id")

	req, err := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/releases/assets/"+itoa(assetID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "token "+fx.token)
	req.Header.Set("Accept", "application/octet-stream; q=1.0, application/json")
	got, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Body.Close() }()
	out, _ := io.ReadAll(got.Body)
	if string(out) != "binary bytes" {
		t.Errorf("octet-stream download served %q, want the raw bytes", out)
	}
}

// TestReleaseDiscussionCategoryAccepted confirms a release create carrying
// discussion_category_name (GoReleaser v2 sets it) is accepted, not rejected,
// even though Githome links no discussion.
func TestReleaseDiscussionCategoryAccepted(t *testing.T) {
	fx := repoServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases", fx.token,
		`{"tag_name":"v2.0.0","discussion_category_name":"Announcements"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("release create status %d, want 201, body %s", resp.StatusCode, body)
	}
}

// TestReleaseGenerateNotesContract pins POST /releases/generate-notes: a
// {name, body} pair in GitHub's "What's Changed" shape, with the previous tag
// auto-detected from the latest published release and overridable by
// previous_tag_name.
func TestReleaseGenerateNotesContract(t *testing.T) {
	fx := repoServer(t)

	// No earlier release: notes from plain history, Full Changelog links the
	// commit list rather than a compare.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases/generate-notes", fx.token,
		`{"tag_name":"v1.0.0"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate-notes status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"name":"v1.0.0"`, `## What's Changed`, `initial commit by Octo Cat`,
		`**Full Changelog**`, `/octocat/hello/commits/v1.0.0`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("generate-notes missing %s: %s", want, body)
		}
	}

	// Publish a release for v0.1.0; the next generate-notes call should cut
	// the range against it automatically.
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases", fx.token,
		`{"tag_name":"v0.1.0"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("release create status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases/generate-notes", fx.token,
		`{"tag_name":"v2.0.0","target_commitish":"master"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate-notes status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"name":"v2.0.0"`, `add the guide by Octo Cat`,
		`/octocat/hello/compare/v0.1.0...v2.0.0`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("auto-previous notes missing %s: %s", want, body)
		}
	}

	// An explicit previous_tag_name wins over the auto-detected one.
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases/generate-notes", fx.token,
		`{"tag_name":"v9.9.9","target_commitish":"master","previous_tag_name":"v0.1.0"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate-notes status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `/octocat/hello/compare/v0.1.0...v9.9.9`) {
		t.Errorf("explicit previous tag not honored: %s", body)
	}

	// tag_name is required.
	resp, _ = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases/generate-notes", fx.token, `{}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing tag_name status %d, want 422", resp.StatusCode)
	}
}

// TestReleaseCreateGenerateNotesFlag pins generate_release_notes on the
// create body: the generated name fills in when none is given, and an explicit
// body is prepended to the generated notes.
func TestReleaseCreateGenerateNotesFlag(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases", fx.token,
		`{"tag_name":"v1.0.0","generate_release_notes":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, body %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"name":"v1.0.0"`, `## What's Changed`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("generated create missing %s: %s", want, body)
		}
	}

	// An explicit body rides in front of the generated notes; an explicit
	// name is kept.
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases", fx.token,
		`{"tag_name":"v0.1.0","name":"point one","body":"the intro","generate_release_notes":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"name":"point one"`) {
		t.Errorf("explicit name clobbered: %s", body)
	}
	if !strings.Contains(string(body), `the intro\n## What's Changed`) {
		t.Errorf("explicit body not prepended: %s", body)
	}
}

// TestReleaseAssetURLsCarryReleaseID pins the R01-3F fix: re-reading an asset
// after upload (GoReleaser's flow) must build browser_download_url from the
// real release id, not a placeholder.
func TestReleaseAssetURLsCarryReleaseID(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/releases", fx.token,
		`{"tag_name":"v1.0.0"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("release create status %d, body %s", resp.StatusCode, body)
	}
	releaseID := jsonInt(t, body, "id")

	resp, body = authedSend(t, fx.srv, http.MethodPost,
		"/api/uploads/repos/octocat/hello/releases/"+itoa(releaseID)+"/assets?name=bin.tar.gz", fx.token,
		"binary bytes")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d, body %s", resp.StatusCode, body)
	}
	assetID := jsonInt(t, body, "id")
	wantDownload := `/octocat/hello/releases/download/` + itoa(releaseID) + `/bin.tar.gz`
	if !strings.Contains(string(body), wantDownload) {
		t.Fatalf("upload response missing %s: %s", wantDownload, body)
	}

	// The re-read is where the placeholder used to leak in as /download/0/.
	resp, body = authedSend(t, fx.srv, http.MethodGet,
		"/repos/octocat/hello/releases/assets/"+itoa(assetID), fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asset get status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), wantDownload) {
		t.Errorf("asset get missing %s: %s", wantDownload, body)
	}
	if strings.Contains(string(body), "/releases/download/0/") {
		t.Errorf("asset get still carries the placeholder release id: %s", body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPatch,
		"/repos/octocat/hello/releases/assets/"+itoa(assetID), fx.token,
		`{"label":"the binary"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asset edit status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), wantDownload) {
		t.Errorf("asset edit missing %s: %s", wantDownload, body)
	}
}
