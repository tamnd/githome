package view

import "testing"

// The patches below are real unified-diff hunk text as the producer emits it:
// they begin at the first @@ with no file header, exactly what FileChange.Patch
// carries. The tests pin the Position math, the line numbering, and the split
// pairing, the three things that must not drift from the API position resolver.

// posOf returns the Position of the first row whose single-cell Text equals want,
// or -1 if no such row exists. It matches on the rendered cell (op byte plus
// text) so a test reads like the diff it checks.
func posOf(rows []DiffRow, want string) int {
	for _, r := range rows {
		if string(r.Text) == want {
			return r.Position
		}
	}
	return -1
}

func TestPositionMath_FirstHunkHeaderIsZero(t *testing.T) {
	patch := "@@ -1,3 +1,4 @@\n one\n two\n+inserted\n three"
	f := BuildDiffFile("f.txt", "", StatusModified, 1, 0, patch, DiffUnified)

	if len(f.Rows) == 0 {
		t.Fatal("no rows built")
	}
	head := f.Rows[0]
	if head.Kind != RowHunkHeader {
		t.Fatalf("first row kind = %v, want RowHunkHeader", head.Kind)
	}
	if head.Position != 0 {
		t.Fatalf("first hunk header Position = %d, want 0 (not counted)", head.Position)
	}
}

func TestPositionMath_CountsFromLineAfterFirstHeader(t *testing.T) {
	patch := "@@ -1,3 +1,4 @@\n one\n two\n+inserted\n three"
	f := BuildDiffFile("f.txt", "", StatusModified, 1, 0, patch, DiffUnified)

	// position 1 = " one", 2 = " two", 3 = "+inserted", 4 = " three".
	cases := []struct {
		text string
		pos  int
	}{
		{" one", 1},
		{" two", 2},
		{"+inserted", 3},
		{" three", 4},
	}
	for _, tc := range cases {
		if got := posOf(f.Rows, tc.text); got != tc.pos {
			t.Errorf("Position of %q = %d, want %d", tc.text, got, tc.pos)
		}
	}
}

func TestPositionMath_SubsequentHeaderIsCounted(t *testing.T) {
	// Two hunks. The first header is position 0. After the first hunk's three
	// lines (positions 1,2,3) the second header is counted at position 4, and the
	// line after it is position 5.
	patch := "" +
		"@@ -1,2 +1,2 @@\n one\n-two\n+too\n" +
		"@@ -10,2 +10,2 @@\n ten\n-eleven\n+elevn"
	f := BuildDiffFile("f.txt", "", StatusModified, 2, 2, patch, DiffUnified)

	var secondHeader *DiffRow
	headers := 0
	for i := range f.Rows {
		if f.Rows[i].Kind == RowHunkHeader {
			headers++
			if headers == 2 {
				secondHeader = &f.Rows[i]
			}
		}
	}
	if secondHeader == nil {
		t.Fatal("second hunk header not found")
	}
	if secondHeader.Position != 4 {
		t.Fatalf("second hunk header Position = %d, want 4 (counted)", secondHeader.Position)
	}
	if got := posOf(f.Rows, " ten"); got != 5 {
		t.Errorf("Position of %q = %d, want 5", " ten", got)
	}
	if got := posOf(f.Rows, "-eleven"); got != 6 {
		t.Errorf("Position of %q = %d, want 6", "-eleven", got)
	}
}

func TestLineNumbering_SidesAdvanceIndependently(t *testing.T) {
	patch := "@@ -5,3 +5,4 @@\n ctx\n-gone\n+added1\n+added2\n more"
	f := BuildDiffFile("f.txt", "", StatusModified, 2, 1, patch, DiffUnified)

	byText := map[string]DiffRow{}
	for _, r := range f.Rows {
		byText[string(r.Text)] = r
	}

	// Context " ctx": old 5, new 5, both sides.
	if r := byText[" ctx"]; r.OldLine != 5 || r.NewLine != 5 || r.Side != SideNone {
		t.Errorf("ctx = old %d new %d side %v, want 5/5/SideNone", r.OldLine, r.NewLine, r.Side)
	}
	// Deletion "-gone": old 6, no new, left side.
	if r := byText["-gone"]; r.OldLine != 6 || r.NewLine != 0 || r.Side != SideLeft {
		t.Errorf("gone = old %d new %d side %v, want 6/0/SideLeft", r.OldLine, r.NewLine, r.Side)
	}
	// Additions advance the new side from 6: added1 new 6, added2 new 7.
	if r := byText["+added1"]; r.NewLine != 6 || r.OldLine != 0 || r.Side != SideRight {
		t.Errorf("added1 = old %d new %d side %v, want 0/6/SideRight", r.OldLine, r.NewLine, r.Side)
	}
	if r := byText["+added2"]; r.NewLine != 7 {
		t.Errorf("added2 NewLine = %d, want 7", r.NewLine)
	}
	// Trailing context " more": old 7 (after the one deletion), new 8.
	if r := byText[" more"]; r.OldLine != 7 || r.NewLine != 8 {
		t.Errorf("more = old %d new %d, want 7/8", r.OldLine, r.NewLine)
	}
}

func TestSplit_PairsDeletionWithAddition(t *testing.T) {
	patch := "@@ -1,2 +1,2 @@\n keep\n-old\n+new"
	f := BuildDiffFile("f.txt", "", StatusModified, 1, 1, patch, DiffSplit)

	var replace *DiffRow
	for i := range f.Rows {
		if f.Rows[i].Kind == RowReplace {
			replace = &f.Rows[i]
			break
		}
	}
	if replace == nil {
		t.Fatal("no RowReplace produced in split mode")
	}
	if string(replace.OldText) != "-old" || string(replace.NewText) != "+new" {
		t.Errorf("replace cells = %q / %q, want -old / +new", replace.OldText, replace.NewText)
	}
	if replace.OldLine != 2 || replace.NewLine != 2 {
		t.Errorf("replace lines = old %d new %d, want 2/2", replace.OldLine, replace.NewLine)
	}
	// The Replace must carry the addition's Position so a split comment anchors
	// to the same offset as in unified. In unified: 0 header, 1 keep, 2 old, 3 new.
	if replace.Position != 3 {
		t.Errorf("replace Position = %d, want 3 (the addition's)", replace.Position)
	}
}

func TestSplit_LeftoverDeletionAndAddition(t *testing.T) {
	// Two deletions, one addition: one pairs, one deletion is left over.
	patch := "@@ -1,3 +1,1 @@\n-a\n-b\n+c"
	f := BuildDiffFile("f.txt", "", StatusModified, 1, 2, patch, DiffSplit)

	var replaces, dels int
	for _, r := range f.Rows {
		switch r.Kind {
		case RowReplace:
			replaces++
		case RowDeletion:
			dels++
		}
	}
	if replaces != 1 {
		t.Errorf("got %d replace rows, want 1", replaces)
	}
	if dels != 1 {
		t.Errorf("got %d leftover deletion rows, want 1", dels)
	}
}

func TestBinaryFile_NoRows(t *testing.T) {
	f := BuildDiffFile("logo.png", "", StatusModified, 1, 0, "", DiffUnified)
	if len(f.Rows) != 0 {
		t.Errorf("binary file produced %d rows, want 0", len(f.Rows))
	}
	if !f.IsBinary {
		t.Error("binary file not flagged IsBinary")
	}
}

func TestRename_NoPatchIsNotBinary(t *testing.T) {
	f := BuildDiffFile("new/name.go", "old/name.go", StatusRenamed, 0, 0, "", DiffUnified)
	if f.IsBinary {
		t.Error("pure rename flagged IsBinary, want false")
	}
	if f.OldPath != "old/name.go" {
		t.Errorf("OldPath = %q, want old/name.go", f.OldPath)
	}
}

func TestNoNewlineMarker_SetsNoEOL(t *testing.T) {
	patch := "@@ -1,1 +1,1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file"
	f := BuildDiffFile("f.txt", "", StatusModified, 1, 1, patch, DiffUnified)

	var sawNoEOL bool
	for _, r := range f.Rows {
		if r.NoEOL {
			sawNoEOL = true
		}
	}
	if !sawNoEOL {
		t.Error("no row carried NoEOL despite the marker")
	}
}

func TestEscaping_AngleBracketsAreEscaped(t *testing.T) {
	patch := "@@ -1,1 +1,1 @@\n+<script>alert(1)</script>"
	f := BuildDiffFile("f.html", "", StatusModified, 1, 0, patch, DiffUnified)

	for _, r := range f.Rows {
		if r.Kind == RowAddition {
			if got := string(r.Text); got != "+&lt;script&gt;alert(1)&lt;/script&gt;" {
				t.Errorf("addition cell = %q, want escaped", got)
			}
		}
	}
}
