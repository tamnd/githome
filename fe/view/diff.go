package view

import (
	"html/template"
	"strconv"
	"strings"
)

// diff.go is the one diff renderer the pull-request surface uses: the Files tab,
// a single commit in a PR, and (later) compare all feed it a file's unified-diff
// patch text and get back a row list. The row list is mode-agnostic: the same
// rows drive the unified and the split template, only the shaping differs.
//
// The load-bearing rule is the Position math (2005/09 section 3.2, owned by
// 2001/08 section 4.1): a 1-based offset counted from the line after the first
// @@ of the file, over context, +, -, and every subsequent @@ header line. A
// review comment anchors by (path, line, side); Position exists only so the row
// stream stays aligned with the offset the API's resolver counts against, never
// as a value the UI sends. The builder is pure (no I/O) so it can be golden
// tested against the same patches the API position resolver consumes, which is
// what keeps the browser's line math and the API's from drifting.
//
// As-built: the live domain yields a per-file unified-diff Patch string (through
// domain.PRService.Files / .Diff, the same bytes the REST .diff media type
// serves), not a pre-parsed hunk list. So this component parses that patch text
// rather than consuming a domain.WalkedFile. It does not recompute the diff: it
// reads the producer's output and only assigns row kinds and positions. The
// patch text carries no file header (git's FileChange.Patch begins at the first
// @@), so parsing starts at the hunks.

// DiffMode is the page-level rendering mode. The same rows render either way.
type DiffMode int

const (
	DiffUnified DiffMode = iota
	DiffSplit
)

// Side is which column a row belongs to. A deletion is LEFT (base), an addition
// RIGHT (head), a context row spans both, and a structural row (hunk header,
// expander) belongs to neither.
type Side int

const (
	SideNone Side = iota
	SideLeft
	SideRight
)

// RowKind tags a row for the template. Replace is a split-only pairing of a
// deletion with the addition opposite it; Expander is a collapsed-context gap.
type RowKind int

const (
	RowContext RowKind = iota
	RowAddition
	RowDeletion
	RowHunkHeader
	RowReplace
	RowExpander
)

// FileStatus is the change kind, GitHub's vocabulary. It drives the status icon
// and the added/removed styling.
type FileStatus string

const (
	StatusAdded      FileStatus = "added"
	StatusModified   FileStatus = "modified"
	StatusRemoved    FileStatus = "removed"
	StatusRenamed    FileStatus = "renamed"
	StatusCopied     FileStatus = "copied"
	StatusTypeChange FileStatus = "changed"
)

// DiffFileVM is one file's diff, the unit the diff_file partial renders. Rows is
// nil for a binary or otherwise special file (which renders a note, not rows) and
// for a file held back as too large to inline.
type DiffFileVM struct {
	Path      string
	OldPath   string // != "" for a rename or copy
	PathHash  string // the #diff-<hash> anchor id
	Status    FileStatus
	Additions int
	Deletions int
	Mode      DiffMode
	Lang      string
	Rows      []DiffRow
	IsBinary  bool
	TooLarge  bool
	HeadSHA   string
	URL       string // the head-side "View file" link

	// OutdatedThreads are this file's review threads whose persisted anchor no
	// longer maps onto a row in the current diff (the line moved or vanished under
	// later commits). They are not placed against a row; the template renders them
	// in a per-file outdated group so the conversation is not lost when the diff
	// churns. The live-anchored threads hang off their DiffRow instead.
	OutdatedThreads []ReviewThreadVM
}

// DiffRow is one rendered line. For unified, Text is the single code cell; for
// split, OldText and NewText are the two code cells. OldLine and NewLine are the
// base- and head-side line numbers (0 when the side does not apply). Position is
// the diff offset; 0 means the row is not commentable.
//
// AnchorSide and AnchorLine are the (side, line) a review comment on this row
// anchors to, the persisted anchor model (never Position). A deletion anchors
// LEFT at its base line, an addition or a context line RIGHT at its head line, so
// a thread the domain stores against (path, line, side) finds its row again.
// Threads are the inline threads the build layer attaches at this row; the pure
// builder leaves the slice nil and the handler fills it after it loads the
// domain's resolved threads.
type DiffRow struct {
	Kind       RowKind
	OldLine    int
	NewLine    int
	Text       template.HTML // unified single cell
	OldText    template.HTML // split left cell
	NewText    template.HTML // split right cell
	Side       Side
	Position   int
	Hunk       int
	NoEOL      bool
	AnchorSide string // "LEFT", "RIGHT", or "" for a structural row
	AnchorLine int    // file line the comment anchors to, 0 when not commentable
	Threads    []ReviewThreadVM
}

// patchHunk is one @@ block parsed out of a file's patch text.
type patchHunk struct {
	header   string
	oldStart int
	newStart int
	lines    []patchLine
}

// patchLine is one body line of a hunk: its op (' ', '+', '-') and its text with
// the leading op byte removed. noEOL marks the "\ No newline at end of file"
// marker that follows the line it applies to.
type patchLine struct {
	op    byte
	text  string
	noEOL bool
}

// BuildDiffFile parses a file's unified-diff patch text and builds its row list
// in the given mode. status, path, oldPath, and the line counts come from the
// producer's per-file record; patch is the hunk text (empty for a binary file).
// A binary file (a non-empty change with no patch) yields no rows and IsBinary.
func BuildDiffFile(path, oldPath string, status FileStatus, additions, deletions int, patch string, mode DiffMode) DiffFileVM {
	f := DiffFileVM{
		Path:      path,
		OldPath:   oldPath,
		PathHash:  diffAnchor(path),
		Status:    status,
		Additions: additions,
		Deletions: deletions,
		Mode:      mode,
	}
	if strings.TrimSpace(patch) == "" {
		// No hunks: a binary file, or a pure rename/mode change with no content
		// delta. Either way there are no rows to render; the partial shows a note.
		f.IsBinary = status != StatusRenamed && status != StatusCopied && (additions > 0 || deletions > 0)
		return f
	}
	f.Rows = buildRows(parsePatch(patch), mode)
	return f
}

// parsePatch splits a file's patch text into hunks. It is tolerant: a line that
// is neither a hunk header nor a recognized body op is treated as context, so a
// malformed patch still renders rather than dropping lines.
func parsePatch(patch string) []patchHunk {
	var hunks []patchHunk
	var cur *patchHunk
	for ln := range strings.SplitSeq(patch, "\n") {
		switch {
		case strings.HasPrefix(ln, "@@"):
			hunks = append(hunks, patchHunk{header: ln})
			cur = &hunks[len(hunks)-1]
			cur.oldStart, cur.newStart = parseHunkStarts(ln)
		case cur == nil:
			// Skip anything before the first hunk header (there should be none, the
			// producer's patch begins at @@, but tolerate a stray prefix line).
		case ln == `\ No newline at end of file`:
			if n := len(cur.lines); n > 0 {
				cur.lines[n-1].noEOL = true
			}
		case len(ln) == 0:
			// A bare empty line inside a hunk is an empty context line.
			cur.lines = append(cur.lines, patchLine{op: ' ', text: ""})
		default:
			op := ln[0]
			if op != ' ' && op != '+' && op != '-' {
				op = ' ' // treat an unexpected leading byte as context text
				cur.lines = append(cur.lines, patchLine{op: op, text: ln})
				continue
			}
			cur.lines = append(cur.lines, patchLine{op: op, text: ln[1:]})
		}
	}
	return hunks
}

// parseHunkStarts reads the old and new starting line numbers from an @@ header
// of the form "@@ -oldStart,oldCount +newStart,newCount @@ optional section".
// A header that does not parse yields zeros, which still renders.
func parseHunkStarts(header string) (oldStart, newStart int) {
	// header: @@ -A,B +C,D @@ ...
	for f := range strings.FieldsSeq(header) {
		if strings.HasPrefix(f, "-") {
			oldStart = leadingInt(f[1:])
		} else if strings.HasPrefix(f, "+") {
			newStart = leadingInt(f[1:])
		}
	}
	return oldStart, newStart
}

// leadingInt parses the number before an optional ",count" suffix.
func leadingInt(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// buildRows turns the parsed hunks into the row list, assigning Position exactly
// per 2001/08 section 4.1: the first file @@ is position 0 and is not counted;
// counting starts on the next line; every subsequent @@ is itself counted and
// carries a Position but is not commentable. After the mode-agnostic pass it
// reshapes change rows for split.
func buildRows(hunks []patchHunk, mode DiffMode) []DiffRow {
	var rows []DiffRow
	pos := 0
	for hi := range hunks {
		h := hunks[hi]
		if hi == 0 {
			rows = append(rows, DiffRow{Kind: RowHunkHeader, Text: escapeHTML(h.header), Side: SideNone, Hunk: hi})
		} else {
			pos++
			rows = append(rows, DiffRow{Kind: RowHunkHeader, Text: escapeHTML(h.header), Side: SideNone, Position: pos, Hunk: hi})
		}
		oldLn, newLn := h.oldStart, h.newStart
		for _, l := range h.lines {
			pos++
			var r DiffRow
			switch l.op {
			case ' ':
				r = DiffRow{Kind: RowContext, OldLine: oldLn, NewLine: newLn, Text: escapeHTML(" " + l.text), Side: SideNone, Position: pos, Hunk: hi, AnchorSide: "RIGHT", AnchorLine: newLn}
				oldLn++
				newLn++
			case '-':
				r = DiffRow{Kind: RowDeletion, OldLine: oldLn, Text: escapeHTML("-" + l.text), Side: SideLeft, Position: pos, Hunk: hi, AnchorSide: "LEFT", AnchorLine: oldLn}
				oldLn++
			case '+':
				r = DiffRow{Kind: RowAddition, NewLine: newLn, Text: escapeHTML("+" + l.text), Side: SideRight, Position: pos, Hunk: hi, AnchorSide: "RIGHT", AnchorLine: newLn}
				newLn++
			}
			if l.noEOL {
				r.NoEOL = true
			}
			rows = append(rows, r)
		}
	}
	if mode == DiffSplit {
		rows = pairForSplit(rows)
	}
	return rows
}

// pairForSplit reshapes contiguous deletion/addition runs into Replace rows for
// the split view: each deletion pairs with the addition opposite it, a leftover
// deletion has an empty right cell, a leftover addition an empty left cell. The
// Position and line numbers are preserved from the source rows; the right cell of
// a Replace carries the addition's NewLine and Position so a comment placed in
// split anchors to the same (side, line) it would in unified.
func pairForSplit(rows []DiffRow) []DiffRow {
	out := make([]DiffRow, 0, len(rows))
	i := 0
	for i < len(rows) {
		r := rows[i]
		if r.Kind != RowDeletion {
			out = append(out, r)
			i++
			continue
		}
		// Gather the contiguous deletion run and the addition run that follows.
		dels := []DiffRow{}
		for i < len(rows) && rows[i].Kind == RowDeletion {
			dels = append(dels, rows[i])
			i++
		}
		adds := []DiffRow{}
		for i < len(rows) && rows[i].Kind == RowAddition {
			adds = append(adds, rows[i])
			i++
		}
		out = append(out, zipReplace(dels, adds)...)
	}
	return out
}

// zipReplace pairs a deletion run with an addition run into Replace rows, then
// emits any leftover as half-filled rows.
func zipReplace(dels, adds []DiffRow) []DiffRow {
	var out []DiffRow
	n := min(len(dels), len(adds))
	for k := range n {
		d, a := dels[k], adds[k]
		out = append(out, DiffRow{
			Kind:       RowReplace,
			OldLine:    d.OldLine,
			NewLine:    a.NewLine,
			OldText:    d.Text,
			NewText:    a.Text,
			Side:       SideRight,
			Position:   a.Position,
			Hunk:       a.Hunk,
			NoEOL:      a.NoEOL,
			AnchorSide: a.AnchorSide,
			AnchorLine: a.AnchorLine,
		})
	}
	for k := n; k < len(dels); k++ {
		out = append(out, dels[k]) // leftover deletion: empty right cell in the template
	}
	for k := n; k < len(adds); k++ {
		out = append(out, adds[k]) // leftover addition: empty left cell
	}
	return out
}

// diffAnchor is the hex SHA-256 of the path, the #diff-<hash> id the file links
// to. It is computed in the build layer; this is a placeholder until that wiring
// lands so the field is never empty.
func diffAnchor(path string) string {
	return "diff-" + path
}

// escapeHTML wraps plain patch text as template.HTML after escaping it. Syntax
// highlighting layers on top of this in the build layer (it replaces the cell
// with a markup-highlighted span stream); the pure builder keeps the text safe
// and faithful so the row math is testable without a highlighter.
func escapeHTML(s string) template.HTML {
	return template.HTML(template.HTMLEscapeString(s))
}
