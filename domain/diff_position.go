package domain

import (
	"strconv"
	"strings"
)

// Diff position resolution: the two ways a review comment anchors to a file's
// unified diff, and the translation between them.
//
// The legacy `position` is a 1-based offset into the file's diff body. Counting
// starts at the line just below the first "@@" hunk header (that line is position
// 1) and increases for every line after it, through context, additions,
// deletions, the "\ No newline" marker, and any later hunk header lines, until
// the next file begins. It is what the older review comment API speaks.
//
// The line/side model is the newer shape: a file line number on a side, RIGHT for
// the head (additions and context) and LEFT for the base (deletions and context).
// A context line exists on both sides at one position; the head line number is
// its RIGHT anchor and the base line number its LEFT anchor.
//
// fileDiff parses one file's patch once and answers both directions, plus whether
// a given anchor is actually in the diff, the check the service makes before it
// lets a comment attach.

const (
	sideLeft  = "LEFT"
	sideRight = "RIGHT"
)

// diffLine is one counted line of a file's diff: its legacy position, the base
// and head line numbers it occupies (zero when it is absent from that side), and
// its kind (' ' context, '+' addition, '-' deletion).
type diffLine struct {
	position int
	oldLine  int
	newLine  int
	kind     byte
}

// fileDiff is a parsed single-file patch, indexed for position and line lookups.
type fileDiff struct {
	lines     []diffLine
	rightByLn map[int]int // head line number -> position
	leftByLn  map[int]int // base line number -> position
	byPos     map[int]diffLine
}

// parseFileDiff reads one file's unified diff (the per-file patch text, with or
// without the leading file headers) and indexes every counted line. Lines before
// the first hunk header carry no position and are skipped.
func parseFileDiff(patch string) *fileDiff {
	d := &fileDiff{
		rightByLn: map[int]int{},
		leftByLn:  map[int]int{},
		byPos:     map[int]diffLine{},
	}
	var (
		position         int
		oldLine, newLine int
		seenHunk         bool
	)
	for _, ln := range strings.Split(patch, "\n") {
		if strings.HasPrefix(ln, "@@") {
			oldStart, newStart := parseHunkHeader(ln)
			if seenHunk {
				// A later hunk header still occupies a position, though nothing
				// anchors to it.
				position++
			}
			oldLine, newLine = oldStart, newStart
			seenHunk = true
			continue
		}
		if !seenHunk {
			continue
		}
		position++
		var kind byte = ' '
		if ln != "" {
			kind = ln[0]
		}
		switch kind {
		case '+':
			dl := diffLine{position: position, newLine: newLine, kind: '+'}
			d.add(dl)
			d.rightByLn[newLine] = position
			newLine++
		case '-':
			dl := diffLine{position: position, oldLine: oldLine, kind: '-'}
			d.add(dl)
			d.leftByLn[oldLine] = position
			oldLine++
		case '\\':
			// "\ No newline at end of file" occupies a position but no line.
			d.add(diffLine{position: position, kind: '\\'})
		default:
			dl := diffLine{position: position, oldLine: oldLine, newLine: newLine, kind: ' '}
			d.add(dl)
			d.rightByLn[newLine] = position
			d.leftByLn[oldLine] = position
			oldLine++
			newLine++
		}
	}
	return d
}

func (d *fileDiff) add(dl diffLine) {
	d.lines = append(d.lines, dl)
	d.byPos[dl.position] = dl
}

// positionFor returns the legacy diff position for a file line on a side, and
// whether that line is part of the diff. side defaults to RIGHT when empty.
func (d *fileDiff) positionFor(line int, side string) (int, bool) {
	if side == sideLeft {
		pos, ok := d.leftByLn[line]
		return pos, ok
	}
	pos, ok := d.rightByLn[line]
	return pos, ok
}

// lineFor returns the file line and side a legacy position anchors to. Additions
// and context resolve to the head line on RIGHT; deletions to the base line on
// LEFT.
func (d *fileDiff) lineFor(position int) (line int, side string, ok bool) {
	dl, ok := d.byPos[position]
	if !ok || dl.kind == '\\' {
		return 0, "", false
	}
	if dl.kind == '-' {
		return dl.oldLine, sideLeft, true
	}
	return dl.newLine, sideRight, true
}

// contains reports whether a file line on a side is part of the diff, the anchor
// validity check the comment path runs.
func (d *fileDiff) contains(line int, side string) bool {
	_, ok := d.positionFor(line, side)
	return ok
}

// parseHunkHeader reads the base and head starting line numbers from an
// "@@ -oldStart,oldLen +newStart,newLen @@" header. A missing count defaults to
// 1, the form git uses for a single-line range.
func parseHunkHeader(h string) (oldStart, newStart int) {
	// Trim to the "-... +..." span between the @@ markers.
	h = strings.TrimPrefix(h, "@@")
	if i := strings.Index(h, "@@"); i >= 0 {
		h = h[:i]
	}
	for _, field := range strings.Fields(h) {
		switch {
		case strings.HasPrefix(field, "-"):
			oldStart = leadingInt(field[1:])
		case strings.HasPrefix(field, "+"):
			newStart = leadingInt(field[1:])
		}
	}
	return oldStart, newStart
}

// leadingInt reads the start line from a "start,len" or "start" range field.
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
