package pulls

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tamnd/githome/domain"
)

// The Checks tab tests ride the shared pulls fixture, which wires the real checks
// service and registers the /checks route the way fe.Mount does. The runs are
// seeded through the same service the tab reads back, against the PR's recorded
// head sha, so the assertions travel the real write-then-read path.

// seedCheck writes one completed check run against the fixture PR's head.
func seedCheck(t *testing.T, fx fixture, name, conclusion, outputTitle string) {
	t.Helper()
	if _, err := fx.checks.CreateCheckRun(context.Background(), fx.ownerPK, fx.owner, fx.repo, domain.CheckRunInput{
		Name:        name,
		HeadSHA:     fx.headSHA,
		Status:      "completed",
		Conclusion:  conclusion,
		DetailsURL:  "https://ci.example.com/" + name,
		OutputTitle: outputTitle,
	}); err != nil {
		t.Fatalf("seed check run %s: %v", name, err)
	}
}

func TestChecksTabGroupsRunsWithStatuses(t *testing.T) {
	fx := newFixture(t)
	seedCheck(t, fx, "build", "success", "Build passed")
	seedCheck(t, fx, "lint", "failure", "Lint found problems")
	if _, err := fx.checks.CreateStatus(context.Background(), fx.ownerPK, fx.owner, fx.repo, fx.headSHA, domain.StatusInput{
		State: "success", Context: "ci/deploy", Description: "Deploy preview ready",
	}); err != nil {
		t.Fatalf("seed commit status: %v", err)
	}

	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/checks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// Both runs render under their suite's app heading with the shared status
	// classes: the passing build green, the failing lint red.
	if !strings.Contains(body, "githome") {
		t.Errorf("checks tab is missing the suite app heading:\n%s", body)
	}
	if !strings.Contains(body, "build") || !strings.Contains(body, "Build passed") {
		t.Errorf("checks tab is missing the build run:\n%s", body)
	}
	if !strings.Contains(body, "lint") || !strings.Contains(body, "Lint found problems") {
		t.Errorf("checks tab is missing the lint run:\n%s", body)
	}
	if !strings.Contains(body, "check-state-success") {
		t.Errorf("checks tab is missing the success state class:\n%s", body)
	}
	if !strings.Contains(body, "check-state-danger") {
		t.Errorf("checks tab is missing the danger state class:\n%s", body)
	}
	// The flat commit status renders in its own section.
	if !strings.Contains(body, "Commit statuses") || !strings.Contains(body, "ci/deploy") {
		t.Errorf("checks tab is missing the commit-status section:\n%s", body)
	}
	// The rollup verdict is worst-first: a failing run makes the pill red.
	if !strings.Contains(body, "Some checks were not successful") {
		t.Errorf("checks tab is missing the failing rollup verdict:\n%s", body)
	}
	// The shell marks the Checks tab current and badges it with the rollup total
	// (two runs plus one status).
	if !strings.Contains(body, "Checks <span class=\"pr-tab-count\">3</span>") {
		t.Errorf("checks tab badge is missing or counts wrong:\n%s", body)
	}
	if !strings.Contains(body, "pr-checks") {
		t.Errorf("checks tab is missing its content container:\n%s", body)
	}
}

func TestChecksTabEmptyBlankslate(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/checks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "No checks reported") {
		t.Errorf("empty checks tab is missing the blankslate:\n%s", body)
	}
	// Nothing reported still badges the tab with a zero count.
	if !strings.Contains(body, "Checks <span class=\"pr-tab-count\">0</span>") {
		t.Errorf("empty checks tab badge is missing:\n%s", body)
	}
}

func TestChecksTabShowsInShellOnOtherTabs(t *testing.T) {
	fx := newFixture(t)
	seedCheck(t, fx, "build", "success", "Build passed")
	_, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum))
	// With the checks service wired, the Conversation tab's shell carries the
	// Checks tab link with the same rollup count.
	if !strings.Contains(body, "/pull/"+itoa(fx.prNum)+"/checks") {
		t.Errorf("conversation shell is missing the Checks tab link:\n%s", body)
	}
	if !strings.Contains(body, "Checks <span class=\"pr-tab-count\">1</span>") {
		t.Errorf("conversation shell is missing the checks count:\n%s", body)
	}
}

func TestChecksTabDetailPane(t *testing.T) {
	fx := newFixture(t)
	seedCheck(t, fx, "build", "success", "Build passed")
	seedCheck(t, fx, "lint", "failure", "Lint found problems")

	// Find the build run's public id through the list page's select link is
	// indirect; read it back through the service instead.
	runs, _, err := fx.checks.ListCheckRuns(context.Background(), 0, fx.owner, fx.repo, fx.headSHA)
	if err != nil {
		t.Fatalf("list check runs: %v", err)
	}
	var buildID int64
	for _, r := range runs {
		if r.Name == "build" {
			buildID = r.ID
		}
	}
	if buildID == 0 {
		t.Fatalf("build run not found among %d runs", len(runs))
	}

	base := "/octocat/hello/pull/" + itoa(fx.prNum) + "/checks"
	resp, body := get(t, fx.srv, base+"?check_run_id="+itoa(buildID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The detail pane opens on the selected run, its row is marked, and the
	// other run stays a plain row.
	if !strings.Contains(body, "check-detail") {
		t.Errorf("detail pane is missing:\n%s", body)
	}
	if !strings.Contains(body, "Build passed") {
		t.Errorf("detail pane is missing the run's output title:\n%s", body)
	}
	if !strings.Contains(body, "is-selected") {
		t.Errorf("selected run's row is not marked:\n%s", body)
	}
	// Every run row links to its own detail.
	if !strings.Contains(body, "check_run_id=") {
		t.Errorf("run rows are missing their select links:\n%s", body)
	}

	// An id that matches nothing at this head renders the plain list.
	resp, body = get(t, fx.srv, base+"?check_run_id=999999")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bogus id status %d, want 200", resp.StatusCode)
	}
	if strings.Contains(body, "check-detail") {
		t.Errorf("bogus check_run_id opened a detail pane:\n%s", body)
	}
}

func TestChecksTabMissingPullNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/hello/pull/9999/checks")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing pull checks status = %d, want 404", resp.StatusCode)
	}
}
