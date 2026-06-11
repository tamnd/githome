package rest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"
)

// readZipEntries returns the archive's file contents keyed by entry name.
func readZipEntries(t *testing.T, body []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(data)
	}
	return out
}

// readTarGzEntries returns the archive's file contents keyed by entry name.
func readTarGzEntries(t *testing.T, body []byte) map[string]string {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = string(data)
	}
	return out
}

func TestZipballArchive(t *testing.T) {
	fx := repoServer(t)
	res, body := get(t, fx.srv, "/repos/octocat/hello/zipball/master")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename="octocat-hello-master.zip"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	files := readZipEntries(t, body)
	if got := files["octocat-hello-master/README.md"]; got != "# Hello\n" {
		t.Errorf("README.md = %q, want %q", got, "# Hello\n")
	}
	if got := files["octocat-hello-master/docs/guide.md"]; got != "guide body\n" {
		t.Errorf("docs/guide.md = %q, want %q", got, "guide body\n")
	}
}

func TestTarballArchive(t *testing.T) {
	fx := repoServer(t)
	res, body := get(t, fx.srv, "/repos/octocat/hello/tarball/master")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/x-gzip" {
		t.Errorf("Content-Type = %q, want application/x-gzip", ct)
	}
	files := readTarGzEntries(t, body)
	if got := files["octocat-hello-master/README.md"]; got != "# Hello\n" {
		t.Errorf("README.md = %q, want %q", got, "# Hello\n")
	}
	if got := files["octocat-hello-master/docs/guide.md"]; got != "guide body\n" {
		t.Errorf("docs/guide.md = %q, want %q", got, "guide body\n")
	}
}

func TestArchiveByTagAndSHA(t *testing.T) {
	fx := repoServer(t)
	res, body := get(t, fx.srv, "/repos/octocat/hello/zipball/v0.1.0")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("tag status = %d: %s", res.StatusCode, body)
	}
	files := readZipEntries(t, body)
	// v0.1.0 points at the first commit, before the guide existed.
	if _, ok := files["octocat-hello-v0.1.0/docs/guide.md"]; ok {
		t.Errorf("tag archive should not contain docs/guide.md")
	}
	if got := files["octocat-hello-v0.1.0/README.md"]; got != "# Hello\n" {
		t.Errorf("README.md = %q, want %q", got, "# Hello\n")
	}

	res, body = get(t, fx.srv, "/repos/octocat/hello/tarball/"+fx.firstSHA)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sha status = %d: %s", res.StatusCode, body)
	}
	prefix := "octocat-hello-" + fx.firstSHA[:7]
	tfiles := readTarGzEntries(t, body)
	if got := tfiles[prefix+"/README.md"]; got != "# Hello\n" {
		t.Errorf("README.md = %q, want %q", got, "# Hello\n")
	}
}

func TestArchiveUnknownRef(t *testing.T) {
	fx := repoServer(t)
	res, _ := get(t, fx.srv, "/repos/octocat/hello/zipball/no-such-ref")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("zipball status = %d, want 404", res.StatusCode)
	}
	res, _ = get(t, fx.srv, "/repos/octocat/hello/tarball/no-such-ref")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("tarball status = %d, want 404", res.StatusCode)
	}
}
