package issues

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/tamnd/githome/domain"
)

// TestIndexMilestoneByTitle pins the milestone: qualifier resolving a title to its
// per-repo number: the seeded v1.0 milestone holds the open issue, so filtering by
// its title lists that issue. A title that matches no milestone narrows to an empty
// list rather than dropping the filter, matching github.com.
func TestIndexMilestoneByTitle(t *testing.T) {
	fx := newFixture(t)
	seedMilestones(t, fx)

	_, body := get(t, fx.srv, "/octocat/hello/issues?q="+queryEscape("is:issue is:open milestone:v1.0"))
	if !strings.Contains(body, "first issue") {
		t.Errorf("milestone:v1.0 filter dropped the issue assigned to it:\n%s", body)
	}

	_, body = get(t, fx.srv, "/octocat/hello/issues?q="+queryEscape("is:issue is:open milestone:does-not-exist"))
	if strings.Contains(body, "first issue") {
		t.Errorf("an unknown milestone: title listed issues instead of narrowing to none:\n%s", body)
	}
}

// TestShowTimelineNoPagerWhenShort confirms a short thread carries no comment
// pager: the zero-value TimelinePager hides the nav entirely.
func TestShowTimelineNoPagerWhenShort(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/issues/"+itoa(fx.openNum))
	if strings.Contains(body, "Comment pagination") {
		t.Errorf("a short thread should not render a comment pager:\n%s", body)
	}
}

// TestShowTimelinePager walks a thread longer than one page: page one shows an
// Older (next) link and no Newer (prev), and page two shows a Newer link. The
// next link is only shown when a one-row probe finds a further comment, so the
// last page carries no dead next.
func TestShowTimelinePager(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	body0 := "a long thread"
	iss, err := fx.issues.CreateIssue(ctx, fx.ownerPK, fx.owner, fx.repo, domain.IssueInput{
		Title: "paged thread", Body: &body0,
	})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	// One past a full page, so page one is full and page two holds the overflow.
	for i := 0; i < showPerPage+1; i++ {
		if _, err := fx.issues.CreateComment(ctx, fx.ownerPK, fx.owner, fx.repo, iss.Number, "comment "+strconv.Itoa(i)); err != nil {
			t.Fatalf("create comment %d: %v", i, err)
		}
	}

	base := "/octocat/hello/issues/" + itoa(iss.Number)

	_, body := get(t, fx.srv, base)
	if !strings.Contains(body, `href="`+base+`?page=2"`) {
		t.Errorf("page one is missing the Older (next) link to page 2:\n%s", body)
	}
	if strings.Contains(body, `class="paginate-prev" href=`) {
		t.Errorf("page one should not carry a Newer (prev) link:\n%s", body)
	}

	_, body = get(t, fx.srv, base+"?page=2")
	if !strings.Contains(body, `class="paginate-prev" href="`+base+`"`) {
		t.Errorf("page two is missing the Newer (prev) link back to page 1:\n%s", body)
	}
	if strings.Contains(body, `class="paginate-next" href=`) {
		t.Errorf("the last page should not carry an Older (next) link:\n%s", body)
	}
}

// queryEscape percent-encodes a q value for a test URL.
func queryEscape(s string) string {
	return strings.NewReplacer(" ", "+", ":", "%3A").Replace(s)
}
