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

// DiffMode values: DiffUnified stacks deletions and additions in one column;
// DiffSplit shows base and head side by side.
const (
	DiffUnified DiffMode = iota
	DiffSplit
)

// Side is which column a row belongs to. A deletion is LEFT (base), an addition
// RIGHT (head), a context row spans both, and a structural row (hunk header,
// expander) belongs to neither.
type Side int

// Side values: SideNone is a structural row that belongs to neither column,
// SideLeft is the base column, and SideRight is the head column.
const (
	SideNone Side = iota
	SideLeft
	SideRight
)

// RowKind tags a row for the template. Replace is a split-only pairing of a
// deletion with the addition opposite it; Expander is a collapsed-context gap.
type RowKind int

// RowKind values: RowContext is an unchanged line shown for context, and the rest
// tag additions, deletions, hunk headers, split replacements, and collapsed gaps.
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

// FileStatus values follow GitHub's change vocabulary, from a newly added file to a
// type change.
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

	// Expand is set on a RowExpander: the collapsed-context gap this row unfolds.
	// It is nil on every other kind.
	Expand *DiffExpand

	// raw is the line content with the op byte stripped, kept on addition and
	// deletion rows so a paired replacement can word-diff the two sides without
	// re-parsing the escaped cell. It is unexported: the template never reads it.
	raw string
}

// DiffContextVM is the data the unfold endpoint renders: the context rows revealed
// from the blob and whether they go into the split table (so the fragment shapes its
// cells the same way the file's table does).
type DiffContextVM struct {
	Rows  []DiffRow
	Split bool
}

// DiffExpand describes the collapsed-context gap an expander row unfolds: the first
// hidden line on each side and how many lines the gap hides, plus the direction the
// unfold grows. The builder fills the line math (it is derivable from the patch); the
// web layer fills URL with the route that fetches those lines from the blob, since
// the pure builder knows neither the owner/repo/number nor the head SHA.
type DiffExpand struct {
	OldStart int    // first hidden base-side line (1-based)
	NewStart int    // first hidden head-side line (1-based)
	Count    int    // hidden line count, derived from the surrounding hunks
	Dir      string // "up" (gap above the first hunk) or "both" (between two hunks)
	URL      string // unfold link; empty in the pure builder, set by the web layer
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

// Per-file inline render caps. A diff page caps how many files it shows, but a
// single generated file (a lockfile, a vendored bundle, minified output) can
// carry hundreds of thousands of lines on its own, and every one of them would
// become an escaped row VM and a table row in the page. Past either bound the
// file renders as too-large with its counts and the view-file link instead of
// rows. The bounds sit well above GitHub's 400-line collapse tier because there
// is no lazy-load fragment yet; when that endpoint lands the threshold can drop
// to match.
const (
	maxDiffFileLines = 2000
	maxDiffFileBytes = 100 << 10
)

// BuildDiffFile parses a file's unified-diff patch text and builds its row list
// in the given mode. status, path, oldPath, and the line counts come from the
// producer's per-file record; patch is the hunk text (empty for a binary file).
// A binary file (a non-empty change with no patch) yields no rows and IsBinary.
// A patch past the per-file caps yields no rows and TooLarge.
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
	if len(patch) > maxDiffFileBytes || strings.Count(patch, "\n") >= maxDiffFileLines {
		f.TooLarge = true
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
	prevOldEnd, prevNewEnd := 0, 0 // last line each side that a previous hunk covered
	for hi := range hunks {
		h := hunks[hi]
		// Emit an expander for the context the patch hides ahead of this hunk: the
		// lines from just past the previous hunk up to just before this one. Before
		// the first hunk the gap runs from line 1; the prefix is unchanged so the two
		// sides share its length. The expander is synthetic, so it carries Position 0
		// and does not advance the patch-offset counter.
		if gap := h.newStart - prevNewEnd - 1; gap > 0 {
			dir := "both"
			if hi == 0 {
				dir = "up"
			}
			rows = append(rows, DiffRow{
				Kind: RowExpander,
				Side: SideNone,
				Hunk: hi,
				Expand: &DiffExpand{
					OldStart: prevOldEnd + 1,
					NewStart: prevNewEnd + 1,
					Count:    gap,
					Dir:      dir,
				},
			})
		}
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
				r = DiffRow{Kind: RowDeletion, OldLine: oldLn, Text: escapeHTML("-" + l.text), Side: SideLeft, Position: pos, Hunk: hi, AnchorSide: "LEFT", AnchorLine: oldLn, raw: l.text}
				oldLn++
			case '+':
				r = DiffRow{Kind: RowAddition, NewLine: newLn, Text: escapeHTML("+" + l.text), Side: SideRight, Position: pos, Hunk: hi, AnchorSide: "RIGHT", AnchorLine: newLn, raw: l.text}
				newLn++
			}
			if l.noEOL {
				r.NoEOL = true
			}
			rows = append(rows, r)
		}
		// oldLn/newLn now sit one past the last line this hunk covered on each side.
		prevOldEnd, prevNewEnd = oldLn-1, newLn-1
	}
	if mode == DiffSplit {
		rows = pairForSplit(rows)
	} else {
		applyIntralineUnified(rows)
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
		oldHTML, newHTML := intralineHighlight(d.raw, a.raw)
		out = append(out, DiffRow{
			Kind:       RowReplace,
			OldLine:    d.OldLine,
			NewLine:    a.NewLine,
			OldText:    oldHTML,
			NewText:    newHTML,
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

// maxIntralineTokens bounds the word-diff: past it the line is long enough that
// the O(n*m) LCS is not worth the spans, so the pair falls back to a whole-line
// tint with no intraline markup. A typical code line is well under this.
const maxIntralineTokens = 400

// applyIntralineUnified word-highlights replaced lines in the unified row stream.
// It finds each contiguous deletion run followed by an addition run, pairs them by
// index (the same pairing the split view uses), and rewrites the paired rows' Text
// with diff-word spans around the bytes that changed. Rows it does not pair (a lone
// deletion or addition, context, headers) keep their whole-line text. It mutates
// rows in place; the Position and line numbers are untouched.
func applyIntralineUnified(rows []DiffRow) {
	i := 0
	for i < len(rows) {
		if rows[i].Kind != RowDeletion {
			i++
			continue
		}
		delStart := i
		for i < len(rows) && rows[i].Kind == RowDeletion {
			i++
		}
		addStart := i
		for i < len(rows) && rows[i].Kind == RowAddition {
			i++
		}
		dels, adds := addStart-delStart, i-addStart
		n := min(dels, adds)
		for k := 0; k < n; k++ {
			d := &rows[delStart+k]
			a := &rows[addStart+k]
			d.Text, a.Text = intralineHighlight(d.raw, a.raw)
		}
	}
}

// intralineHighlight word-diffs a replaced line pair and returns the two op-prefixed
// cell HTMLs with a diff-word span around each side's changed run. It tokenizes both
// sides, takes the longest common token subsequence, and wraps the tokens unique to
// each side. When the two lines share no non-whitespace token it is a rewrite, not an
// edit, so it skips the spans and returns the plain whole-line tint — the same call
// GitHub makes, and what keeps a wholly different pair from lighting up end to end.
func intralineHighlight(oldRaw, newRaw string) (oldHTML, newHTML template.HTML) {
	oldTok := tokenizeWords(oldRaw)
	newTok := tokenizeWords(newRaw)
	if len(oldTok) > maxIntralineTokens || len(newTok) > maxIntralineTokens {
		return escapeHTML("-" + oldRaw), escapeHTML("+" + newRaw)
	}
	oldKeep, newKeep := lcsMask(oldTok, newTok)
	if !sharesWord(oldTok, oldKeep) {
		return escapeHTML("-" + oldRaw), escapeHTML("+" + newRaw)
	}
	return template.HTML("-") + wrapChanged(oldTok, oldKeep),
		template.HTML("+") + wrapChanged(newTok, newKeep)
}

// tokenizeWords splits a line into word tokens: a maximal run of identifier bytes
// (letters, digits, underscore), a maximal run of whitespace, or one other byte on
// its own. Punctuation standing alone keeps the diff granular ("a.b" vs "a.c" pins
// the changed "b"/"c"), and identifier runs keep whole names together.
func tokenizeWords(s string) []string {
	var toks []string
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case isWordByte(c):
			j := i
			for j < len(s) && isWordByte(s[j]) {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		case c == ' ' || c == '\t':
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		default:
			toks = append(toks, s[i:i+1])
			i++
		}
	}
	return toks
}

// isWordByte reports whether a byte is part of an identifier run.
func isWordByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// lcsMask returns, for each side, a bool per token marking the tokens that lie on a
// longest common subsequence (true = unchanged, false = changed). It is the standard
// LCS DP over token equality, then a backtrack that sets the kept positions.
func lcsMask(a, b []string) (aKeep, bKeep []bool) {
	n, m := len(a), len(b)
	aKeep = make([]bool, n)
	bKeep = make([]bool, m)
	if n == 0 || m == 0 {
		return aKeep, bKeep
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			aKeep[i], bKeep[j] = true, true
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return aKeep, bKeep
}

// sharesWord reports whether any kept token is a non-whitespace word, the test for
// "these two lines are an edit of each other" rather than a full rewrite.
func sharesWord(toks []string, keep []bool) bool {
	for i, k := range keep {
		if k && strings.TrimSpace(toks[i]) != "" {
			return true
		}
	}
	return false
}

// wrapChanged escapes the token stream and wraps each maximal run of changed tokens
// in a diff-word span. Kept tokens render as plain escaped text, so only the bytes
// that differ carry the darker word tint.
func wrapChanged(toks []string, keep []bool) template.HTML {
	var b strings.Builder
	i := 0
	for i < len(toks) {
		if keep[i] {
			b.WriteString(template.HTMLEscapeString(toks[i]))
			i++
			continue
		}
		j := i
		var run strings.Builder
		for j < len(toks) && !keep[j] {
			run.WriteString(toks[j])
			j++
		}
		b.WriteString(`<span class="diff-word">`)
		b.WriteString(template.HTMLEscapeString(run.String()))
		b.WriteString(`</span>`)
		i = j
	}
	return template.HTML(b.String())
}

// BuildContextRows builds the rows an unfold reveals: count lines of the file
// starting at the given base/head line, taken from the head blob's content. The two
// sides advance together because an unfolded line is unchanged context, so OldLine
// and NewLine stay in lockstep. The rows carry Position 0: a line that was not in the
// patch is read-only context, never a comment anchor, which keeps the patch-offset
// space the review API resolves against untouched. A range past the end of the file
// stops early, so a too-eager unfold yields what exists and no more.
func BuildContextRows(content string, oldStart, newStart, count int) []DiffRow {
	lines := splitLines(content)
	rows := make([]DiffRow, 0, count)
	for i := 0; i < count; i++ {
		ln := newStart + i // 1-based head-side line number
		if ln < 1 || ln > len(lines) {
			break
		}
		rows = append(rows, DiffRow{
			Kind:    RowContext,
			OldLine: oldStart + i,
			NewLine: ln,
			Text:    escapeHTML(" " + lines[ln-1]),
			Side:    SideNone,
		})
	}
	return rows
}

// splitLines splits file content into lines, dropping the empty trailing element a
// final newline leaves so the last real line is not shadowed by a phantom blank.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// escapeHTML wraps plain patch text as template.HTML after escaping it. Syntax
// highlighting layers on top of this in the build layer (it replaces the cell
// with a markup-highlighted span stream); the pure builder keeps the text safe
// and faithful so the row math is testable without a highlighter.
func escapeHTML(s string) template.HTML {
	return template.HTML(template.HTMLEscapeString(s))
}
