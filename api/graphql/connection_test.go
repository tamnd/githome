package graphql

import "testing"

// TestIssuePageWindow exercises the forward and backward window math the
// connections page with. gh leans on the backward form: its issueCommentLast
// fragment selects comments(last: 1) and the status rollup selects
// commits(last: 1).
func TestIssuePageWindow(t *testing.T) {
	i32 := func(n int32) *int32 { return &n }

	cases := []struct {
		name               string
		first, last        *int32
		after, before      *string
		total              int
		wantStart, wantEnd int
	}{
		{name: "default forward", total: 10, wantStart: 0, wantEnd: 10},
		{name: "first 3", first: i32(3), total: 10, wantStart: 0, wantEnd: 3},
		{name: "first beyond total", first: i32(30), total: 4, wantStart: 0, wantEnd: 4},
		{name: "last 1", last: i32(1), total: 5, wantStart: 4, wantEnd: 5},
		{name: "last 3 of 2", last: i32(3), total: 2, wantStart: 0, wantEnd: 2},
		{name: "last 0", last: i32(0), total: 5, wantStart: 5, wantEnd: 5},
		{name: "last on empty", last: i32(1), total: 0, wantStart: 0, wantEnd: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := issuePageArgs(tc.first, tc.after, tc.last, tc.before)
			if err != nil {
				t.Fatalf("issuePageArgs: %v", err)
			}
			start, end := p.window(tc.total)
			if start != tc.wantStart || end != tc.wantEnd {
				t.Errorf("window(%d) = [%d, %d), want [%d, %d)", tc.total, start, end, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

// TestIssuePageArgsLimits confirms the over-limit and negative cases reject
// with GitHub's wording for last as they already did for first.
func TestIssuePageArgsLimits(t *testing.T) {
	i32 := func(n int32) *int32 { return &n }
	if _, err := issuePageArgs(nil, nil, i32(101), nil); err == nil {
		t.Error("last over the cap did not error")
	}
	if _, err := issuePageArgs(nil, nil, i32(-1), nil); err == nil {
		t.Error("negative last did not error")
	}
}
