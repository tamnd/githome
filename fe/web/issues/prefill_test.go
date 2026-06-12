package issues

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tamnd/githome/domain"
)

// TestNewIssuePrefillQuery seeds the form straight from the query: the title
// and body land in their fields, and the labels, assignees, and milestone ride
// hidden inputs so the create POST applies them.
func TestNewIssuePrefillQuery(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/issues/new?title=Crash+on+save&body=It+broke.&labels=bug,ui&assignees=octocat&milestone=3")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `value="Crash on save"`) {
		t.Errorf("prefill lost the title:\n%s", body)
	}
	if !strings.Contains(body, "It broke.") {
		t.Errorf("prefill lost the body:\n%s", body)
	}
	for _, field := range []string{
		`name="labels" value="bug"`,
		`name="labels" value="ui"`,
		`name="assignees" value="octocat"`,
		`name="milestone" value="3"`,
	} {
		if !strings.Contains(body, field) {
			t.Errorf("prefill is missing the hidden field %s:\n%s", field, body)
		}
	}
}

// TestNewIssuePrefillBadValuesIgnored keeps the form usable when the prefill
// is junk: a non-numeric milestone drops, empty list items drop, and the page
// still renders.
func TestNewIssuePrefillBadValuesIgnored(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/issues/new?milestone=banana&labels=,,")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if strings.Contains(body, `name="milestone"`) {
		t.Errorf("junk milestone leaked into the form:\n%s", body)
	}
	if strings.Contains(body, `name="labels"`) {
		t.Errorf("empty labels leaked into the form:\n%s", body)
	}
}

// TestNewIssuePrefillTemplate loads a Markdown template out of
// .github/ISSUE_TEMPLATE on the default branch: the front matter fills the
// title and labels, the rest becomes the body, and an explicit query value
// wins over the template's.
func TestNewIssuePrefillTemplate(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	repo, err := fx.repos.GetRepo(ctx, 0, fx.owner, fx.repo)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	tpl := "---\n" +
		"name: Bug report\n" +
		"title: \"[Bug]: \"\n" +
		"labels: [bug, needs-triage]\n" +
		"assignees: octocat\n" +
		"---\n" +
		"### What happened?\n\nTell us.\n"
	if _, err := fx.repos.WriteFile(repo, domain.WriteFileInput{
		Path:        ".github/ISSUE_TEMPLATE/bug.md",
		Content:     []byte(tpl),
		Message:     "add the bug template",
		AuthorName:  "Octo Cat",
		AuthorEmail: "octo@example.com",
		Branch:      "master",
	}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp, body := get(t, fx.srv, "/octocat/hello/issues/new?template=bug.md")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `value="[Bug]: "`) {
		t.Errorf("template title did not seed the form:\n%s", body)
	}
	if !strings.Contains(body, "### What happened?") {
		t.Errorf("template body did not seed the form:\n%s", body)
	}
	if strings.Contains(body, "name: Bug report") {
		t.Errorf("front matter leaked into the body:\n%s", body)
	}
	for _, field := range []string{
		`name="labels" value="bug"`,
		`name="labels" value="needs-triage"`,
		`name="assignees" value="octocat"`,
	} {
		if !strings.Contains(body, field) {
			t.Errorf("template metadata is missing the hidden field %s:\n%s", field, body)
		}
	}

	// The query wins over the template for a field both set.
	_, body = get(t, fx.srv, "/octocat/hello/issues/new?template=bug.md&title=My+own+title")
	if !strings.Contains(body, `value="My own title"`) || strings.Contains(body, `value="[Bug]: "`) {
		t.Errorf("query title did not win over the template's:\n%s", body)
	}
}

// TestNewIssuePrefillTemplateGuards keeps a hostile template name from walking
// the tree: a path or a dotfile renders the blank form, never an error.
func TestNewIssuePrefillTemplateGuards(t *testing.T) {
	fx := newFixture(t)
	for _, tpl := range []string{"../../README.md", ".hidden", "missing.md"} {
		resp, body := get(t, fx.srv, "/octocat/hello/issues/new?template="+tpl)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("template=%q status %d, want 200", tpl, resp.StatusCode)
		}
		if !strings.Contains(body, "New issue") {
			t.Errorf("template=%q did not render the blank form", tpl)
		}
	}
}
