package jsondiff

import "testing"

func compareJSON(t *testing.T, want, got string) []Diff {
	t.Helper()
	wv := decode(t, "want", []byte(want))
	gv := decode(t, "got", []byte(got))
	return Compare(wv, gv, Default("git.test.internal"))
}

func TestIdenticalIsClean(t *testing.T) {
	diffs := compareJSON(t, `{"a":1,"b":"x","c":[1,2]}`, `{"a":1,"b":"x","c":[1,2]}`)
	if len(diffs) != 0 {
		t.Fatalf("expected no diffs, got %v", diffs)
	}
}

func TestMissingKey(t *testing.T) {
	diffs := compareJSON(t, `{"a":1,"b":2}`, `{"a":1}`)
	if len(diffs) != 1 || diffs[0].Kind != MissingKey {
		t.Fatalf("expected one missing-key diff, got %v", diffs)
	}
}

func TestExtraKey(t *testing.T) {
	diffs := compareJSON(t, `{"a":1}`, `{"a":1,"z":9}`)
	if len(diffs) != 1 || diffs[0].Kind != ExtraKey {
		t.Fatalf("expected one extra-key diff, got %v", diffs)
	}
}

func TestTypeMismatch(t *testing.T) {
	diffs := compareJSON(t, `{"a":1}`, `{"a":"1"}`)
	if len(diffs) != 1 || diffs[0].Kind != TypeMismatch {
		t.Fatalf("expected one type-mismatch diff, got %v", diffs)
	}
}

func TestNullability(t *testing.T) {
	diffs := compareJSON(t, `{"a":null}`, `{"a":1}`)
	if len(diffs) != 1 || diffs[0].Kind != Nullability {
		t.Fatalf("expected one nullability diff, got %v", diffs)
	}
}

func TestIgnoredValueKeys(t *testing.T) {
	// id, *_id, *_at, and the rate-limit counters differ in value but must still
	// be present with the right type.
	diffs := compareJSON(t,
		`{"id":1,"node_id":"A","created_at":"2020-01-01T00:00:00Z","reset":1}`,
		`{"id":999,"node_id":"ZZ","created_at":"2026-06-04T00:00:00Z","reset":42}`)
	if len(diffs) != 0 {
		t.Fatalf("expected ignored values to be clean, got %v", diffs)
	}
}

func TestIgnoredCamelCaseKeys(t *testing.T) {
	// GraphQL timestamps (createdAt) and numeric ids (databaseId) vary by
	// instance and are ignored like their snake_case REST counterparts, while a
	// value-bearing field that merely ends in those letters is still compared.
	diffs := compareJSON(t,
		`{"createdAt":"2020-01-01T00:00:00Z","databaseId":1,"oid":"abc","format":"tarball"}`,
		`{"createdAt":"2026-06-04T00:00:00Z","databaseId":999,"oid":"abc","format":"tarball"}`)
	if len(diffs) != 0 {
		t.Fatalf("expected camelCase timestamp and id values to be ignored, got %v", diffs)
	}

	// oid is a content-addressed value and must still be compared.
	diffs = compareJSON(t, `{"oid":"abc"}`, `{"oid":"def"}`)
	if len(diffs) != 1 || diffs[0].Kind != ValueMismatch {
		t.Fatalf("expected oid value-mismatch, got %v", diffs)
	}
}

func TestURLHostNormalized(t *testing.T) {
	diffs := compareJSON(t,
		`{"url":"https://github.com/octocat/Hello-World"}`,
		`{"url":"https://git.test.internal/octocat/Hello-World"}`)
	if len(diffs) != 0 {
		t.Fatalf("expected host-normalized URLs to match, got %v", diffs)
	}
}

func TestURLPathStillCompared(t *testing.T) {
	diffs := compareJSON(t,
		`{"url":"https://github.com/octocat/Hello-World"}`,
		`{"url":"https://git.test.internal/octocat/Goodbye"}`)
	if len(diffs) != 1 || diffs[0].Kind != ValueMismatch {
		t.Fatalf("expected a path value-mismatch, got %v", diffs)
	}
}

func TestLengthMismatch(t *testing.T) {
	diffs := compareJSON(t, `{"a":[1,2]}`, `{"a":[1]}`)
	if len(diffs) != 1 || diffs[0].Kind != LengthMismatch {
		t.Fatalf("expected a length-mismatch, got %v", diffs)
	}
}
