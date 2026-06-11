package webmw

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzip.go is the response-compression layer (spec 2005/16 section 9): an HTML
// page is mostly repeated markup and compresses 8-10x, so this is the single
// biggest wire win on the front. Compression is negotiated per request
// (Accept-Encoding) and applied per response by content type, so an image, a
// tarball, or a git pack passes through untouched while HTML, CSS, JS, SVG, and
// JSON ship compressed.

// gzipWriters pools gzip writers across requests; building the deflate state
// per response would cost more than some of the small pages it compresses.
var gzipWriters = sync.Pool{
	New: func() any {
		// BestSpeed: the page byte budget cares about wire size class, not the
		// last few percent, and the CPU budget cares a lot.
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	},
}

// compressibleTypes are the content types worth compressing. Everything absent
// (images, archives, git packs, fonts) is either already compressed or binary
// enough that gzip is wasted work.
var compressibleTypes = map[string]bool{
	"text/html":                 true,
	"text/css":                  true,
	"text/plain":                true,
	"text/javascript":           true,
	"text/xml":                  true,
	"application/javascript":    true,
	"application/json":          true,
	"application/xml":           true,
	"application/manifest+json": true,
	"image/svg+xml":             true,
}

// Gzip wraps h with gzip response compression. Every response gains
// Vary: Accept-Encoding (the same URL serves two encodings, so a cache must key
// on the header); the body is compressed only when the client offers gzip, the
// response carries a compressible content type, no Content-Encoding is already
// set, and the status has a body.
func Gzip(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r) {
			h.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		h.ServeHTTP(gw, r)
	})
}

// acceptsGzip reports whether the request negotiates gzip. A quality of zero
// ("gzip;q=0") is an explicit refusal.
func acceptsGzip(r *http.Request) bool {
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		token, params, _ := strings.Cut(part, ";")
		token = strings.TrimSpace(token)
		if token != "gzip" && token != "*" {
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

// gzipResponseWriter defers the compress-or-not decision to the first
// WriteHeader, when the content type is known, then either streams through a
// pooled gzip writer or passes bytes through untouched.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.wroteHeader = true
	h := w.Header()
	if shouldCompress(code, h) {
		// The compressed length is unknowable up front; the server falls back
		// to chunked transfer.
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		gz := gzipWriters.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
	}
	w.ResponseWriter.WriteHeader(code)
}

// shouldCompress decides at header-write time: a body-bearing status, a
// compressible declared type, and no encoding already applied.
func shouldCompress(code int, h http.Header) bool {
	if code == http.StatusNoContent || code == http.StatusNotModified || (code >= 100 && code < 200) {
		return false
	}
	if h.Get("Content-Encoding") != "" {
		return false
	}
	ct := h.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return compressibleTypes[strings.TrimSpace(strings.ToLower(ct))]
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		// Mirror net/http: an unset content type is sniffed from the first
		// bytes so the compress decision sees what the client would.
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// close flushes and returns the pooled gzip writer once the handler is done.
func (w *gzipResponseWriter) close() {
	if w.gz == nil {
		return
	}
	_ = w.gz.Close()
	w.gz.Reset(nil)
	gzipWriters.Put(w.gz)
	w.gz = nil
}

// Flush completes any pending gzip block before flushing the connection, so a
// streamed page still reaches the client incrementally when compressed.
func (w *gzipResponseWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack passes through so a future upgrade endpoint behind this layer keeps
// working; a hijacked connection is the caller's to manage, uncompressed.
func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
