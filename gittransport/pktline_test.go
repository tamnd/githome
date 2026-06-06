package gittransport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWritePktString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"# service=git-upload-pack\n", "001e# service=git-upload-pack\n"},
		{"hello", "0009hello"},
		{"", "0004"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := writePktString(&buf, tc.in); err != nil {
			t.Fatalf("writePktString(%q): %v", tc.in, err)
		}
		if got := buf.String(); got != tc.want {
			t.Errorf("writePktString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWritePktStringTooLong(t *testing.T) {
	big := strings.Repeat("x", maxPktLen)
	if err := writePktString(&bytes.Buffer{}, big); err == nil {
		t.Fatal("expected error for oversized payload, got nil")
	}
}

// TestFlushWriterFlushesOnWrite locks the streaming invariant: every Write to
// flushWriter must also call Flush on the underlying ResponseWriter so pack
// data reaches the git client incrementally rather than after the subprocess
// exits.
func TestFlushWriterFlushesOnWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &flushWriter{w: rec}

	if _, err := fw.Write([]byte("pack")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// httptest.ResponseRecorder implements http.Flusher; Flushed is set by Flush.
	if !rec.Flushed {
		t.Error("flushWriter.Write did not call Flush; pack data would be buffered until EOF")
	}
}

// TestFlushWriterFallsBackGracefully confirms flushWriter still writes even
// when the underlying writer does not implement http.Flusher.
func TestFlushWriterFallsBackGracefully(t *testing.T) {
	var buf bytes.Buffer
	// http.ResponseWriter is required; wrap buf in a minimal stub.
	fw := &flushWriter{w: &nonFlushingWriter{buf: &buf}}
	if _, err := fw.Write([]byte("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "data" {
		t.Errorf("got %q, want %q", got, "data")
	}
}

// nonFlushingWriter is a minimal http.ResponseWriter that does not implement
// http.Flusher, used to confirm flushWriter degrades gracefully.
type nonFlushingWriter struct {
	buf     *bytes.Buffer
	headers http.Header
}

func (n *nonFlushingWriter) Header() http.Header {
	if n.headers == nil {
		n.headers = make(http.Header)
	}
	return n.headers
}
func (n *nonFlushingWriter) Write(p []byte) (int, error) { return n.buf.Write(p) }
func (n *nonFlushingWriter) WriteHeader(_ int)           {}

// BenchmarkWritePktString confirms the pkt-line header is framed with
// zero heap allocations per call once the pool is warm.
func BenchmarkWritePktString(b *testing.B) {
	var buf bytes.Buffer
	s := "# service=git-upload-pack\n"
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		if err := writePktString(&buf, s); err != nil {
			b.Fatal(err)
		}
	}
}
