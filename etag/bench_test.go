package etag

import (
	"bytes"
	"testing"
)

// BenchmarkETagWeak_8KB measures computing a weak ETag over an 8 KB body.
// Target: <= 5 µs/op.
func BenchmarkETagWeak_8KB(b *testing.B) {
	body := bytes.Repeat([]byte(`{"id":1,"login":"octocat","node_id":"U_Ag=="}`), 186) // ~8 KB
	body = body[:8192]
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = Weak(body)
	}
}

// BenchmarkETagWeak_1KB measures computing a weak ETag over a 1 KB body.
func BenchmarkETagWeak_1KB(b *testing.B) {
	body := bytes.Repeat([]byte(`{"id":1,"login":"octocat"}`), 40) // ~1 KB
	body = body[:1024]
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = Weak(body)
	}
}

// BenchmarkETagVersion measures computing a version ETag from a seed, id, and markers.
// Target: <= 500 ns/op.
func BenchmarkETagVersion(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Version("Issue", 42, 1717200000000000000, 7)
	}
}

// BenchmarkETagVersionNoMarkers measures Version with no extra markers.
func BenchmarkETagVersionNoMarkers(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Version("Repository", 50)
	}
}

// BenchmarkETagMatch measures the fast path where the header matches the tag.
func BenchmarkETagMatch(b *testing.B) {
	tag := Weak([]byte(`{"id":1,"login":"octocat"}`))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = Match(tag, tag)
	}
}
