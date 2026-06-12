package issues

import (
	"context"
	"path"
	"strconv"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/view"
)

// prefill.go reads the documented new-issue prefill query (spec section 4.5):
// ?title=&body=&labels=&assignees=&milestone= seed the form directly, and
// ?template= loads a Markdown issue template out of .github/ISSUE_TEMPLATE on
// the default branch. The query wins over the template for any field both set,
// matching github.com. The prefill only seeds the form; the create handler
// applies the metadata through the same service path a hand-filled form takes.

// maxIssueTemplateBytes bounds how much of a template file seeds the body, so
// a pathological blob cannot balloon the form page.
const maxIssueTemplateBytes = 64 << 10

// prefillNewIssue seeds the blank form from the query. An unknown template or
// an unreadable repository quietly leaves the form blank: the prefill is a
// convenience, never a gate.
func (h *Handlers) prefillNewIssue(c *mizu.Ctx, repo *domain.Repo, vm *view.NewIssueVM) {
	q := c.Request().URL.Query()
	vm.Title = strings.TrimSpace(q.Get("title"))
	vm.Body = q.Get("body")
	vm.Labels = splitPrefillList(q.Get("labels"))
	vm.Assignees = splitPrefillList(q.Get("assignees"))
	if n, err := strconv.ParseInt(q.Get("milestone"), 10, 64); err == nil && n > 0 {
		vm.Milestone = strconv.FormatInt(n, 10)
	}
	if tpl := q.Get("template"); tpl != "" {
		h.applyIssueTemplate(c.Context(), repo, tpl, vm)
	}
}

// applyIssueTemplate reads .github/ISSUE_TEMPLATE/{name} at the default branch
// and folds it into the form: the body is the template text with its YAML
// front matter stripped, and the front matter's title, labels, and assignees
// fill any field the query left empty.
func (h *Handlers) applyIssueTemplate(ctx context.Context, repo *domain.Repo, name string, vm *view.NewIssueVM) {
	// The template is one file name inside the template directory, never a
	// path of its own.
	if name != path.Base(name) || strings.HasPrefix(name, ".") {
		return
	}
	res, err := h.repos.Contents(repo, ".github/ISSUE_TEMPLATE/"+name, repo.DefaultBranch)
	if err != nil || res.IsDir || res.File == nil {
		return
	}
	content := res.File.Content
	if len(content) > maxIssueTemplateBytes {
		content = content[:maxIssueTemplateBytes]
	}
	front, body := splitFrontMatter(string(content))
	if vm.Body == "" {
		vm.Body = body
	}
	if vm.Title == "" {
		// unquoteYAML trims around the scalar but keeps quoted whitespace, so a
		// template title like "[Bug]: " holds its trailing space.
		vm.Title = unquoteYAML(front["title"])
	}
	if len(vm.Labels) == 0 {
		vm.Labels = splitYAMLList(front["labels"])
	}
	if len(vm.Assignees) == 0 {
		vm.Assignees = splitYAMLList(front["assignees"])
	}
}

// splitFrontMatter cuts a leading "---" YAML block off a Markdown template,
// returning its top-level scalar lines as a key/value map and the remaining
// body. A template with no front matter is all body.
func splitFrontMatter(s string) (map[string]string, string) {
	front := map[string]string{}
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return front, s
	}
	block, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return front, s
	}
	// The closing fence owns its line; the body starts after its newline.
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}
	for line := range strings.SplitSeq(block, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != k || k == "" {
			continue // nested or malformed lines are not top-level scalars
		}
		front[k] = strings.TrimSpace(v)
	}
	return front, strings.TrimLeft(body, "\n")
}

// splitYAMLList reads a front-matter list value in either inline form:
// "bug, help wanted" or ["bug", "help wanted"].
func splitYAMLList(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	return splitList(v, func(s string) string { return unquoteYAML(s) })
}

// unquoteYAML strips one layer of single or double quotes off a scalar.
func unquoteYAML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// splitPrefillList reads a comma-separated query value into its trimmed,
// non-empty items.
func splitPrefillList(v string) []string {
	return splitList(v, strings.TrimSpace)
}

// splitList splits on commas and maps each item through clean, dropping
// empties.
func splitList(v string, clean func(string) string) []string {
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if c := clean(item); strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}
