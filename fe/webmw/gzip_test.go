package webmw

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gzipEcho serves a fixed body under the given content type, optionally with a
// preset Content-Encoding, behind the Gzip layer.
func gzipEcho(contentType, encoding, body string) http.Handler {
	return Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		if encoding != "" {
			w.Header().Set("Content-Encoding", encoding)
		}
		_, _ = io.WriteString(w, body)
	}))
}

func gzipGet(t *testing.T, h http.Handler, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGzipCompressesNegotiatedHTML(t *testing.T) {
	body := strings.Repeat("<p>hello world</p>\n", 200)
	rec := gzipGet(t, gzipEcho("text/html; charset=utf-8", "", body), "gzip, deflate, br")

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding listed", rec.Header().Values("Vary"))
	}
	if rec.Body.Len() >= len(body) {
		t.Fatalf("compressed body (%d) not smaller than source (%d)", rec.Body.Len(), len(body))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if string(out) != body {
		t.Fatal("round-trip body mismatch")
	}
}

func TestGzipSkipsClientsWithoutGzip(t *testing.T) {
	body := strings.Repeat("plain\n", 100)
	for _, ae := range []string{"", "identity", "gzip;q=0", "br"} {
		rec := gzipGet(t, gzipEcho("text/html", "", body), ae)
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Accept-Encoding %q: Content-Encoding = %q, want none", ae, got)
		}
		if rec.Body.String() != body {
			t.Fatalf("Accept-Encoding %q: body altered", ae)
		}
		if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
			t.Fatalf("Accept-Encoding %q: Vary must still be set", ae)
		}
	}
}

func TestGzipSkipsIncompressibleTypes(t *testing.T) {
	for _, ct := range []string{"image/png", "application/gzip", "application/octet-stream", "application/x-git-upload-pack-result"} {
		rec := gzipGet(t, gzipEcho(ct, "", "binarybinarybinary"), "gzip")
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("%s: Content-Encoding = %q, want none", ct, got)
		}
	}
}

func TestGzipSkipsAlreadyEncodedResponses(t *testing.T) {
	rec := gzipGet(t, gzipEcho("text/css", "br", "precompressed"), "gzip")
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want the preset br untouched", got)
	}
	if rec.Body.String() != "precompressed" {
		t.Fatal("already-encoded body must pass through")
	}
}

func TestGzipCompressesSVGAndCSS(t *testing.T) {
	for _, ct := range []string{"image/svg+xml", "text/css", "application/javascript", "application/json"} {
		rec := gzipGet(t, gzipEcho(ct, "", strings.Repeat("data ", 100)), "gzip")
		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("%s: Content-Encoding = %q, want gzip", ct, got)
		}
	}
}
