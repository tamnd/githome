package domain

import "testing"

// The diff used across the position cases: two hunks, a deletion replaced by two
// additions, then a second hunk with one addition. The positions are counted by
// hand from the spec's rule (position 1 is the line below the first @@, and every
// line after it counts, later hunk headers included).
//
//	pos  line
//	  -  @@ -1,3 +1,4 @@
//	  1   line one              (context: old 1, new 1)
//	  2  -line two              (delete: old 2)
//	  3  +line two changed      (add:    new 2)
//	  4  +line three new        (add:    new 3)
//	  5   line four             (context: old 3, new 4)
//	  6  @@ -10,2 +11,3 @@       (header occupies a position, anchors nothing)
//	  7   ctx a                 (context: old 10, new 11)
//	  8  +added in second hunk  (add:    new 12)
//	  9   ctx b                 (context: old 11, new 13)
const twoHunkPatch = `@@ -1,3 +1,4 @@
 line one
-line two
+line two changed
+line three new
 line four
@@ -10,2 +11,3 @@
 ctx a
+added in second hunk
 ctx b`

func TestPositionForLineSide(t *testing.T) {
	d := parseFileDiff(twoHunkPatch)
	cases := []struct {
		name string
		line int
		side string
		want int
	}{
		{"added line resolves to its position", 2, sideRight, 3},
		{"second added line", 3, sideRight, 4},
		{"deleted line on the left", 2, sideLeft, 2},
		{"context anchors on the right by head line", 4, sideRight, 5},
		{"context also anchors on the left by base line", 3, sideLeft, 5},
		{"first context line left side", 1, sideLeft, 1},
		{"second hunk context right", 11, sideRight, 7},
		{"second hunk context left", 10, sideLeft, 7},
		{"second hunk addition", 12, sideRight, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := d.positionFor(tc.line, tc.side)
			if !ok {
				t.Fatalf("positionFor(%d, %s) not found", tc.line, tc.side)
			}
			if got != tc.want {
				t.Errorf("positionFor(%d, %s) = %d, want %d", tc.line, tc.side, got, tc.want)
			}
		})
	}
}

func TestLineForPosition(t *testing.T) {
	d := parseFileDiff(twoHunkPatch)
	cases := []struct {
		pos      int
		wantLine int
		wantSide string
		wantOK   bool
	}{
		{1, 1, sideRight, true},  // context resolves to head/RIGHT
		{2, 2, sideLeft, true},   // deletion resolves to base/LEFT
		{3, 2, sideRight, true},  // addition
		{4, 3, sideRight, true},  // addition
		{5, 4, sideRight, true},  // context -> head line
		{6, 0, "", false},        // hunk header, anchors nothing
		{8, 12, sideRight, true}, // second hunk addition
		{99, 0, "", false},       // past the end
	}
	for _, tc := range cases {
		line, side, ok := d.lineFor(tc.pos)
		if ok != tc.wantOK {
			t.Errorf("lineFor(%d) ok = %v, want %v", tc.pos, ok, tc.wantOK)
			continue
		}
		if ok && (line != tc.wantLine || side != tc.wantSide) {
			t.Errorf("lineFor(%d) = (%d, %s), want (%d, %s)", tc.pos, line, side, tc.wantLine, tc.wantSide)
		}
	}
}

// Round-tripping every resolvable position back through positionFor must land on
// the same position: the two directions agree.
func TestPositionLineRoundTrip(t *testing.T) {
	d := parseFileDiff(twoHunkPatch)
	for pos := 1; pos <= 9; pos++ {
		line, side, ok := d.lineFor(pos)
		if !ok {
			continue // hunk headers have no anchor
		}
		back, ok := d.positionFor(line, side)
		if !ok || back != pos {
			t.Errorf("round trip pos %d -> (%d,%s) -> %d (ok=%v)", pos, line, side, back, ok)
		}
	}
}

func TestContainsRejectsOffDiffAnchors(t *testing.T) {
	d := parseFileDiff(twoHunkPatch)
	if !d.contains(2, sideRight) {
		t.Error("added line 2 RIGHT should be in the diff")
	}
	if d.contains(500, sideRight) {
		t.Error("line 500 RIGHT is not in the diff")
	}
	// An unchanged file region outside any hunk is not commentable.
	if d.contains(7, sideRight) {
		t.Error("head line 7 falls between hunks and is not in the diff")
	}
}

// A pure-addition patch (a new file) has no left side at all.
func TestNewFilePatchHasNoLeftSide(t *testing.T) {
	d := parseFileDiff("@@ -0,0 +1,2 @@\n+first\n+second")
	if p, ok := d.positionFor(1, sideRight); !ok || p != 1 {
		t.Errorf("positionFor(1, RIGHT) = %d, %v; want 1, true", p, ok)
	}
	if p, ok := d.positionFor(2, sideRight); !ok || p != 2 {
		t.Errorf("positionFor(2, RIGHT) = %d, %v; want 2, true", p, ok)
	}
	if d.contains(1, sideLeft) {
		t.Error("a new file has no base side to anchor on")
	}
}

func TestParseHunkHeaderRanges(t *testing.T) {
	cases := []struct {
		header           string
		wantOld, wantNew int
	}{
		{"@@ -1,3 +1,4 @@", 1, 1},
		{"@@ -10,2 +11,3 @@", 10, 11},
		{"@@ -0,0 +1,2 @@", 0, 1},
		{"@@ -5 +5 @@", 5, 5},                  // single-line ranges omit the count
		{"@@ -1,3 +1,4 @@ func foo() {", 1, 1}, // section heading after the markers
	}
	for _, tc := range cases {
		old, nw := parseHunkHeader(tc.header)
		if old != tc.wantOld || nw != tc.wantNew {
			t.Errorf("parseHunkHeader(%q) = (%d, %d), want (%d, %d)", tc.header, old, nw, tc.wantOld, tc.wantNew)
		}
	}
}
