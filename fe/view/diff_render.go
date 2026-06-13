package view

import (
	"html/template"
	"strconv"
)

// diff_render.go holds the small presentation accessors the diff template reads so
// the template never compares against an unexported integer constant. The row kind
// and the file status are integers and a string the build layer sets; these turn
// them into the booleans and CSS tokens a template can branch on, the same way
// PRState exposes a flattened StateVM. They add no state; they read the row.

// IsHunkHeader reports whether the row is an @@ hunk header, which the template
// renders as a full-width section divider rather than a code line.
func (r DiffRow) IsHunkHeader() bool { return r.Kind == RowHunkHeader }

// IsAddition reports whether the row is an added line (head side).
func (r DiffRow) IsAddition() bool { return r.Kind == RowAddition }

// IsDeletion reports whether the row is a removed line (base side).
func (r DiffRow) IsDeletion() bool { return r.Kind == RowDeletion }

// IsContext reports whether the row is an unchanged context line.
func (r DiffRow) IsContext() bool { return r.Kind == RowContext }

// IsReplace reports whether the row is a split-only paired change: a deletion in
// the base column lined up with the addition that replaced it in the head column.
// It exists only in split mode, where the builder zips contiguous deletion and
// addition runs together.
func (r DiffRow) IsReplace() bool { return r.Kind == RowReplace }

// IsExpander reports whether the row is a collapsed-context gap: the template
// renders it as an unfold control rather than a code line.
func (r DiffRow) IsExpander() bool { return r.Kind == RowExpander }

// IsSplit reports whether the file renders side-by-side, the base column left of
// the head column. The same row list drives unified and split; only the template
// shaping differs, so the partial branches on this.
func (f DiffFileVM) IsSplit() bool { return f.Mode == DiffSplit }

// LeftText is the base-column code cell in split mode: the deleted text for a
// replacement, the line itself for a deletion or context, and empty for an
// addition (which has no base side).
func (r DiffRow) LeftText() template.HTML {
	switch r.Kind {
	case RowReplace:
		return r.OldText
	case RowAddition:
		return ""
	default:
		return r.Text
	}
}

// RightText is the head-column code cell in split mode: the added text for a
// replacement, the line itself for an addition or context, and empty for a
// deletion (which has no head side).
func (r DiffRow) RightText() template.HTML {
	switch r.Kind {
	case RowReplace:
		return r.NewText
	case RowDeletion:
		return ""
	default:
		return r.Text
	}
}

// LeftClass is the CSS modifier for the base-column cell: a deletion tint for a
// removed or replaced line, an empty filler opposite an addition, context
// otherwise.
func (r DiffRow) LeftClass() string {
	switch r.Kind {
	case RowDeletion, RowReplace:
		return "deletion"
	case RowAddition:
		return "empty"
	default:
		return "context"
	}
}

// RightClass is the CSS modifier for the head-column cell: an addition tint for an
// added or replaced line, an empty filler opposite a deletion, context otherwise.
func (r DiffRow) RightClass() string {
	switch r.Kind {
	case RowAddition, RowReplace:
		return "addition"
	case RowDeletion:
		return "empty"
	default:
		return "context"
	}
}

// CommentsLeft reports whether a split-mode inline composer for this row belongs
// in the base column, the case for a deletion the viewer anchors a comment to.
func (r DiffRow) CommentsLeft() bool { return r.Commentable() && r.AnchorSide == "LEFT" }

// CommentsRight reports whether a split-mode inline composer for this row belongs
// in the head column, the case for an addition or context line.
func (r DiffRow) CommentsRight() bool { return r.Commentable() && r.AnchorSide == "RIGHT" }

// Commentable reports whether a review comment could anchor to this row: it has a
// counted position and is a code line, not a structural header. F4 renders the
// diff read-only, so the template uses this only to mark anchorable lines; the
// inline composer arrives in F5.
func (r DiffRow) Commentable() bool {
	return r.Position > 0 && r.Kind != RowHunkHeader
}

// CellClass is the CSS modifier the template puts on the row's code cell so the
// stylesheet colors additions, deletions, and context distinctly.
func (r DiffRow) CellClass() string {
	switch r.Kind {
	case RowAddition:
		return "addition"
	case RowDeletion:
		return "deletion"
	case RowHunkHeader:
		return "hunk"
	default:
		return "context"
	}
}

// OldLineLabel is the base-side line number for the gutter, blank when the row has
// no base side (an addition or a hunk header).
func (r DiffRow) OldLineLabel() string {
	if r.OldLine == 0 || r.Kind == RowAddition || r.Kind == RowHunkHeader {
		return ""
	}
	return strconv.Itoa(r.OldLine)
}

// NewLineLabel is the head-side line number for the gutter, blank when the row has
// no head side (a deletion or a hunk header).
func (r DiffRow) NewLineLabel() string {
	if r.NewLine == 0 || r.Kind == RowDeletion || r.Kind == RowHunkHeader {
		return ""
	}
	return strconv.Itoa(r.NewLine)
}

// StatusIcon is the octicon for a file's change kind, every name registered in the
// icon set (the coverage test guarantees it).
func (s FileStatus) StatusIcon() string {
	switch s {
	case StatusAdded:
		return "diff-added"
	case StatusRemoved:
		return "diff-removed"
	case StatusRenamed, StatusCopied:
		return "diff-renamed"
	default:
		return "diff-modified"
	}
}

// StatusLabel is the human word for a file's change kind, for the file-row aria
// label and tooltip.
func (s FileStatus) StatusLabel() string {
	switch s {
	case StatusAdded:
		return "added"
	case StatusRemoved:
		return "removed"
	case StatusRenamed:
		return "renamed"
	case StatusCopied:
		return "copied"
	case StatusTypeChange:
		return "changed"
	default:
		return "modified"
	}
}
