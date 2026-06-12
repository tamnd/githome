package repo

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

// zipNames opens body as a zip and returns the entry names.
func zipNames(t *testing.T, body string) []string {
	t.Helper()
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

// tarGzNames decompresses body and returns the tar entry names.
func tarGzNames(t *testing.T, body string) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestArchiveZipForBranch(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/archive/master.zip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type %q, want application/zip", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="octocat-hello-master.zip"` {
		t.Errorf("Content-Disposition %q", cd)
	}
	// Every entry sits under the owner-repo-ref prefix, and the head tree is
	// fully present: master is at the second commit, so the guide is in.
	names := zipNames(t, body)
	var sawReadme, sawGuide bool
	for _, n := range names {
		if !strings.HasPrefix(n, "octocat-hello-master/") {
			t.Errorf("entry %q outside the prefix directory", n)
		}
		sawReadme = sawReadme || n == "octocat-hello-master/README.md"
		sawGuide = sawGuide || n == "octocat-hello-master/docs/guide.md"
	}
	if !sawReadme || !sawGuide {
		t.Errorf("zip is missing expected entries, got %v", names)
	}
}

func TestArchiveTarGzForQualifiedTag(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/archive/refs/tags/v1.0.0.tar.gz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-gzip" {
		t.Errorf("Content-Type %q, want application/x-gzip", ct)
	}
	// The qualified prefix drops from the directory label and the filename.
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="octocat-hello-v1.0.0.tar.gz"` {
		t.Errorf("Content-Disposition %q", cd)
	}
	// v1.0.0 tags the first commit, so the archive has the README but no docs
	// directory: the ref resolved as a tag, not as the branch head.
	names := tarGzNames(t, body)
	var sawReadme bool
	for _, n := range names {
		if strings.Contains(n, "docs/guide.md") {
			t.Errorf("tag archive contains second-commit file %q", n)
		}
		sawReadme = sawReadme || n == "octocat-hello-v1.0.0/README.md"
	}
	if !sawReadme {
		t.Errorf("tar.gz is missing the README, got %v", names)
	}
}

func TestArchiveNotFound(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{
		"/octocat/hello/archive/nosuchref.zip", // unresolvable ref
		"/octocat/hello/archive/master.rar",    // unknown format suffix
		"/octocat/hello/archive/.zip",          // empty ref
		"/octocat/blank/archive/main.tar.gz",   // empty repository
		"/octocat/secret/archive/master.zip",   // private repo, anonymous viewer
	} {
		resp, _ := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestArchiveRefLabel(t *testing.T) {
	cases := map[string]string{
		"master":                   "master",
		"refs/heads/feat/x":        "feat-x",
		"refs/tags/v1.0.0":         "v1.0.0",
		"feature/deep/branch":      "feature-deep-branch",
		strings.Repeat("a1b2", 10): strings.Repeat("a1b2", 10)[:7],
	}
	for ref, want := range cases {
		if got := archiveRefLabel(ref); got != want {
			t.Errorf("archiveRefLabel(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestTagsPageLinksArchives(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/tags")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	for _, href := range []string{
		`href="/octocat/hello/archive/refs/tags/v1.0.0.zip"`,
		`href="/octocat/hello/archive/refs/tags/v1.0.0.tar.gz"`,
	} {
		if !strings.Contains(body, href) {
			t.Errorf("tags page is missing download link %s", href)
		}
	}
}
