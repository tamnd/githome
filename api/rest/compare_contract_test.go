package rest

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// compareGet issues a compare GET with an optional Accept override.
func compareGet(t *testing.T, fx repoFixture, basehead, accept string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/compare/"+basehead, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "token "+fx.token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// TestCompareContract pins the full compare body against a golden: one commit
// ahead, the base and merge base commits populated, the file diff carrying
// sha, patch, and html blob URLs, and the permalink/diff/patch links present.
// The fixture's object ids are deterministic, so the golden stays stable.
func TestCompareContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "compare.golden.json", "/repos/octocat/hello/compare/"+fx.firstSHA+"...master")
}

// TestCompareStatuses walks the four status values: ahead, behind, identical,
// and diverged once a side branch gains its own commit.
func TestCompareStatuses(t *testing.T) {
	fx := repoServer(t)

	resp, body := compareGet(t, fx, fx.firstSHA+"...master", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ahead status %d, body %s", resp.StatusCode, body)
	}
	obj := decodeObject(t, body)
	if obj["status"] != "ahead" || obj["ahead_by"] != float64(1) || obj["behind_by"] != float64(0) {
		t.Fatalf("ahead compare = status %v ahead %v behind %v", obj["status"], obj["ahead_by"], obj["behind_by"])
	}
	base, _ := obj["base_commit"].(map[string]any)
	if base == nil || base["sha"] != fx.firstSHA {
		t.Fatalf("base_commit = %v, want %s", obj["base_commit"], fx.firstSHA)
	}
	mb, _ := obj["merge_base_commit"].(map[string]any)
	if mb == nil || mb["sha"] != fx.firstSHA {
		t.Fatalf("merge_base_commit = %v, want %s", obj["merge_base_commit"], fx.firstSHA)
	}

	resp, body = compareGet(t, fx, "master..."+fx.firstSHA, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("behind status %d, body %s", resp.StatusCode, body)
	}
	obj = decodeObject(t, body)
	if obj["status"] != "behind" || obj["ahead_by"] != float64(0) || obj["behind_by"] != float64(1) {
		t.Fatalf("behind compare = status %v ahead %v behind %v", obj["status"], obj["ahead_by"], obj["behind_by"])
	}

	resp, body = compareGet(t, fx, "master...master", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("identical status %d, body %s", resp.StatusCode, body)
	}
	if obj = decodeObject(t, body); obj["status"] != "identical" {
		t.Fatalf("identical compare = status %v", obj["status"])
	}

	// A branch from the first commit plus one commit of its own diverges.
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"refs/heads/feature","sha":"`+fx.firstSHA+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create ref status %d, body %s", resp.StatusCode, body)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte("side work\n"))
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/contents/side.txt", fx.token,
		`{"message":"add side.txt","content":"`+b64+`","branch":"feature"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("side commit status %d, body %s", resp.StatusCode, body)
	}
	resp, body = compareGet(t, fx, "master...feature", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diverged status %d, body %s", resp.StatusCode, body)
	}
	obj = decodeObject(t, body)
	if obj["status"] != "diverged" || obj["ahead_by"] != float64(1) || obj["behind_by"] != float64(1) {
		t.Fatalf("diverged compare = status %v ahead %v behind %v", obj["status"], obj["ahead_by"], obj["behind_by"])
	}
}

// TestCompareMedia covers the Accept negotiation: the diff media type answers
// the raw unified diff, the patch media type the mbox series.
func TestCompareMedia(t *testing.T) {
	fx := repoServer(t)

	resp, body := compareGet(t, fx, fx.firstSHA+"...master", "application/vnd.github.diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff status %d, body %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "vnd.github.diff") {
		t.Fatalf("diff content-type = %s", ct)
	}
	if !strings.HasPrefix(string(body), "diff --git") {
		t.Fatalf("diff body = %.60s", body)
	}

	resp, body = compareGet(t, fx, fx.firstSHA+"...master", "application/vnd.github.patch")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d, body %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "vnd.github.patch") {
		t.Fatalf("patch content-type = %s", ct)
	}
	if !strings.HasPrefix(string(body), "From ") {
		t.Fatalf("patch body = %.60s", body)
	}
}

// TestComparePaging covers the commit window: a page past the only commit is
// an empty commits list, and the files array rides only on the first page.
func TestComparePaging(t *testing.T) {
	fx := repoServer(t)

	resp, body := compareGet(t, fx, fx.firstSHA+"...master?per_page=1&page=2", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 2 status %d, body %s", resp.StatusCode, body)
	}
	obj := decodeObject(t, body)
	commits, _ := obj["commits"].([]any)
	if len(commits) != 0 {
		t.Fatalf("page 2 commits = %v, want empty", obj["commits"])
	}
	files, _ := obj["files"].([]any)
	if len(files) != 0 {
		t.Fatalf("page 2 files = %v, want empty", obj["files"])
	}
}

// TestCompareErrors covers the refusals: a basehead without a range separator
// is 422 and an unknown ref on either side is 404.
func TestCompareErrors(t *testing.T) {
	fx := repoServer(t)

	resp, body := compareGet(t, fx, "master", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("no-range status %d, want 422, body %s", resp.StatusCode, body)
	}
	resp, body = compareGet(t, fx, "master...nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad head status %d, want 404, body %s", resp.StatusCode, body)
	}
}

// TestCompareTwoDot covers the direct two-dot form: the file list is the diff
// of the two trees themselves, so a change only on the base side surfaces as
// a removal in the comparison.
func TestCompareTwoDot(t *testing.T) {
	fx := repoServer(t)

	resp, body := compareGet(t, fx, "master.."+fx.firstSHA, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("two-dot status %d, body %s", resp.StatusCode, body)
	}
	obj := decodeObject(t, body)
	files, _ := obj["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("two-dot files = %s", body)
	}
	f, _ := files[0].(map[string]any)
	if f["filename"] != "docs/guide.md" || f["status"] != "removed" {
		t.Fatalf("two-dot file = %v", f)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["permalink_url"]; !ok {
		t.Fatal("permalink_url missing")
	}
}
