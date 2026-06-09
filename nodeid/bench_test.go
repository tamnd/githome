package nodeid

import "testing"

// BenchmarkNodeIDEncode measures encoding a single node ID in both formats.
// Target: <= 200 ns/op, <= 2 allocs/op.
func BenchmarkNodeIDEncode(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Encode(KindIssue, 123456, FormatNew)
	}
}

// BenchmarkNodeIDEncodeLegacy measures encoding in the legacy base64 format.
func BenchmarkNodeIDEncodeLegacy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Encode(KindIssue, 123456, FormatLegacy)
	}
}

// BenchmarkNodeIDDecode measures decoding a new-format node ID.
// Target: <= 200 ns/op, <= 2 allocs/op.
func BenchmarkNodeIDDecode(b *testing.B) {
	encoded := Encode(KindIssue, 123456, FormatNew)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = Decode(encoded)
	}
}

// BenchmarkNodeIDDecodeLegacy measures decoding a legacy-format node ID.
func BenchmarkNodeIDDecodeLegacy(b *testing.B) {
	encoded := Encode(KindIssue, 123456, FormatLegacy)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = Decode(encoded)
	}
}

// BenchmarkNodeIDRoundTrip measures a full encode+decode cycle.
func BenchmarkNodeIDRoundTrip(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		enc := Encode(KindPullRequest, 987654, FormatNew)
		_, _, _ = Decode(enc)
	}
}
