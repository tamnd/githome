package nodeid

import "testing"

func TestGitObjectRoundTrip(t *testing.T) {
	cases := []struct {
		tag string
		id  int64
		oid string
	}{
		{"commit", 1, "0123456789abcdef0123456789abcdef01234567"},
		{"blob", 42, "fedcba9876543210fedcba9876543210fedcba98"},
		{"tree", 9_007_199_254_740_991, "1111111111111111111111111111111111111111"},
		{"ref", 0, "refs/heads/main"},
	}
	for _, tc := range cases {
		enc := EncodeGitObject(tc.tag, tc.id, tc.oid)
		if enc == "" {
			t.Fatalf("EncodeGitObject(%q, %d, %q) returned empty", tc.tag, tc.id, tc.oid)
		}
		tag, id, oid, err := DecodeGitObject(enc)
		if err != nil {
			t.Fatalf("DecodeGitObject(%q): %v", enc, err)
		}
		if tag != tc.tag || id != tc.id || oid != tc.oid {
			t.Fatalf("round trip: want (%q,%d,%q) got (%q,%d,%q) via %q",
				tc.tag, tc.id, tc.oid, tag, id, oid, enc)
		}
	}
}

func TestGitObjectPrefixes(t *testing.T) {
	want := map[string]string{"commit": "C_", "blob": "B_", "ref": "REF_"}
	for tag, prefix := range want {
		enc := EncodeGitObject(tag, 7, "abc")
		if got := enc[:len(prefix)]; got != prefix {
			t.Errorf("%s node id should start with %q, got %q", tag, prefix, enc)
		}
	}
}

func TestEncodeGitObjectUnknownTag(t *testing.T) {
	if got := EncodeGitObject("nope", 1, "abc"); got != "" {
		t.Fatalf("unknown tag should encode to empty, got %q", got)
	}
}

func TestDecodeGitObjectRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "noseparator", "X_abc", "C_!!!"} {
		if _, _, _, err := DecodeGitObject(bad); err == nil {
			t.Errorf("DecodeGitObject(%q) should have failed", bad)
		}
	}
}
