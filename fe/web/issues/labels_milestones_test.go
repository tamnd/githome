package issues

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/githome/domain"
)

// seedMilestones adds an open milestone (due in the past, so it shows overdue)
// holding the fixture's open issue, and a closed empty one. It returns the open
// milestone's number.
func seedMilestones(t *testing.T, fx fixture) int64 {
	t.Helper()
	ctx := context.Background()

	title := "v1.0"
	desc := "the first stable cut"
	due := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	m, err := fx.issues.CreateMilestone(ctx, fx.ownerPK, fx.owner, fx.repo, domain.MilestoneInput{
		Title: &title, Description: &desc, DueOn: &due,
	})
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	if _, err := fx.issues.EditIssue(ctx, fx.ownerPK, fx.owner, fx.repo, fx.openNum, domain.IssuePatch{
		MilestoneNumber: &m.Number,
	}); err != nil {
		t.Fatalf("assign milestone: %v", err)
	}

	doneTitle := "v0.9"
	closedState := "closed"
	if _, err := fx.issues.CreateMilestone(ctx, fx.ownerPK, fx.owner, fx.repo, domain.MilestoneInput{
		Title: &doneTitle, State: &closedState,
	}); err != nil {
		t.Fatalf("create closed milestone: %v", err)
	}
	return m.Number
}

func TestLabelsPage(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/labels")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The seeded bug label renders as the same color chip the issue rows use,
	// linking to the filtered issues index.
	if !strings.Contains(body, "--label-r:215;--label-g:58;--label-b:74") {
		t.Errorf("labels page is missing the bug chip color:\n%s", body)
	}
	if !strings.Contains(body, "/octocat/hello/issues?q=") {
		t.Error("label chip does not link to the filtered issues index")
	}
	if !strings.Contains(body, "1 label") || strings.Contains(body, "1 labels") {
		t.Errorf("labels page count line is off:\n%s", body)
	}
	// The cross-link to milestones mirrors github.com's paired bar.
	if !strings.Contains(body, `href="/octocat/hello/milestones"`) {
		t.Error("labels page is missing the milestones cross-link")
	}
}

func TestMilestonesList(t *testing.T) {
	fx := newFixture(t)
	num := seedMilestones(t, fx)

	resp, body := get(t, fx.srv, "/octocat/hello/milestones")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `href="/octocat/hello/milestone/`+strconv.FormatInt(num, 10)+`"`) {
		t.Errorf("open tab is missing the v1.0 milestone link:\n%s", body)
	}
	if !strings.Contains(body, "1 Open") || !strings.Contains(body, "1 Closed") {
		t.Errorf("state tabs carry the wrong counts:\n%s", body)
	}
	// One open issue, none closed: 0% complete, and the due date renders with
	// the overdue marker (the fixture due date is in the past).
	if !strings.Contains(body, "0% complete") {
		t.Errorf("milestone row is missing the progress line:\n%s", body)
	}
	if !strings.Contains(body, "Jan 31, 2026") || !strings.Contains(body, "Past due") {
		t.Errorf("milestone row is missing the due line:\n%s", body)
	}
	// The closed milestone stays off the open tab and shows on ?state=closed.
	if strings.Contains(body, "v0.9") {
		t.Error("closed milestone leaked onto the open tab")
	}
	_, body = get(t, fx.srv, "/octocat/hello/milestones?state=closed")
	if !strings.Contains(body, "v0.9") {
		t.Errorf("closed tab is missing the closed milestone:\n%s", body)
	}
}

func TestMilestoneDetail(t *testing.T) {
	fx := newFixture(t)
	num := seedMilestones(t, fx)
	base := "/octocat/hello/milestone/" + strconv.FormatInt(num, 10)

	resp, body := get(t, fx.srv, base)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "v1.0") || !strings.Contains(body, "the first stable cut") {
		t.Errorf("milestone page is missing its header block:\n%s", body)
	}
	// The open tab lists the assigned issue as a normal issue row.
	if !strings.Contains(body, "first issue") {
		t.Errorf("milestone page is missing its open issue:\n%s", body)
	}
	// The closed tab is empty: the fixture's closed issue has no milestone.
	_, body = get(t, fx.srv, base+"?closed=1")
	if strings.Contains(body, "first issue") {
		t.Error("closed tab lists an open issue")
	}
	if !strings.Contains(body, "No closed issues in this milestone.") {
		t.Errorf("closed tab is missing the blankslate:\n%s", body)
	}
}

func TestMilestoneNotFound(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{
		"/octocat/hello/milestone/999",
		"/octocat/hello/milestone/zero",
		"/octocat/secret/milestones", // private repo, anonymous viewer
		"/octocat/secret/labels",
	} {
		resp, _ := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestIssueRowMilestoneChipLinksPage pins the chip retarget: an issue row's
// milestone chip goes to the milestone page, not a query-filtered index.
func TestIssueRowMilestoneChipLinksPage(t *testing.T) {
	fx := newFixture(t)
	num := seedMilestones(t, fx)
	_, body := get(t, fx.srv, "/octocat/hello/issues")
	want := `href="/octocat/hello/milestone/` + strconv.FormatInt(num, 10) + `"`
	if !strings.Contains(body, want) {
		t.Errorf("issue row milestone chip does not link the milestone page (want %s):\n%s", want, body)
	}
}
