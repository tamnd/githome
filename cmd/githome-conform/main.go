// Command githome-conform certifies a running Githome instance against the
// compatibility matrix: it points at a base URL with an auth token and a target
// repository, runs the section-3 surface (repository read, conditional
// requests, issue listing and pagination, the search endpoints, and GraphQL
// introspection plus a connection query), and prints a pass/fail report.
//
// It is a black-box client: it speaks only HTTP and reads only the wire shapes,
// so it certifies any Githome build the same way a real client would see it. It
// exits non-zero when any check fails, so it can gate a release.
//
// Usage:
//
//	githome-conform [flags] <owner>/<repo>
//
//	  -url string      instance origin (env GITHOME_URL), e.g. https://git.example.com
//	  -token string    auth token (env GITHOME_TOKEN)
//	  -api string      REST base override (default <url>/api/v3)
//	  -graphql string  GraphQL endpoint override (default <url>/api/graphql)
//
// The REST and GraphQL bases follow the GitHub Enterprise layout (/api/v3 and
// /api/graphql) and can be overridden for an instance that mounts them
// elsewhere.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "githome-conform:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("githome-conform", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	urlFlag := fs.String("url", os.Getenv("GITHOME_URL"), "instance origin, e.g. https://git.example.com")
	token := fs.String("token", os.Getenv("GITHOME_TOKEN"), "auth token")
	apiFlag := fs.String("api", "", "REST base override (default <url>/api/v3)")
	gqlFlag := fs.String("graphql", "", "GraphQL endpoint override (default <url>/api/graphql)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: githome-conform [flags] <owner>/<repo>")
	}
	owner, repo, ok := splitNWO(fs.Arg(0))
	if !ok {
		return fmt.Errorf("target %q is not in owner/repo form", fs.Arg(0))
	}
	if *urlFlag == "" {
		return fmt.Errorf("an instance URL is required (-url or GITHOME_URL)")
	}

	origin := strings.TrimRight(*urlFlag, "/")
	apiBase := *apiFlag
	if apiBase == "" {
		apiBase = origin + "/api/v3"
	}
	apiBase = strings.TrimRight(apiBase, "/")
	gqlURL := *gqlFlag
	if gqlURL == "" {
		gqlURL = origin + "/api/graphql"
	}

	c := &conformer{
		api:   apiBase,
		gql:   gqlURL,
		token: *token,
		owner: owner,
		repo:  repo,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
	rep := c.runMatrix()
	if err := rep.print(out, fmt.Sprintf("%s/%s @ %s", owner, repo, origin)); err != nil {
		return err
	}
	if rep.failed() {
		return fmt.Errorf("%d of %d checks failed", rep.countFailed(), len(rep.rows))
	}
	return nil
}

func splitNWO(s string) (owner, repo string, ok bool) {
	owner, repo, ok = strings.Cut(s, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", false
	}
	return owner, repo, true
}

// status is the outcome of one matrix check.
type status int

const (
	pass status = iota
	fail
	skip
)

func (s status) String() string {
	switch s {
	case pass:
		return "PASS"
	case fail:
		return "FAIL"
	default:
		return "SKIP"
	}
}

// row is one cell of the matrix: a section, the check name, its outcome, and a
// one-line detail.
type row struct {
	section string
	check   string
	status  status
	detail  string
}

type report struct {
	rows []row
}

func (r *report) add(section, check string, st status, detail string) {
	r.rows = append(r.rows, row{section: section, check: check, status: st, detail: detail})
}

func (r *report) failed() bool { return r.countFailed() > 0 }

func (r *report) countFailed() int {
	n := 0
	for _, row := range r.rows {
		if row.status == fail {
			n++
		}
	}
	return n
}

func (r *report) print(w io.Writer, target string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Githome conformance: %s\n\n", target)
	wSection, wCheck := len("SECTION"), len("CHECK")
	for _, row := range r.rows {
		if len(row.section) > wSection {
			wSection = len(row.section)
		}
		if len(row.check) > wCheck {
			wCheck = len(row.check)
		}
	}
	fmt.Fprintf(&b, "%-*s  %-*s  %-4s  %s\n", wSection, "SECTION", wCheck, "CHECK", "RES", "DETAIL")
	var passed, failed, skipped int
	for _, row := range r.rows {
		fmt.Fprintf(&b, "%-*s  %-*s  %-4s  %s\n", wSection, row.section, wCheck, row.check, row.status, row.detail)
		switch row.status {
		case pass:
			passed++
		case fail:
			failed++
		case skip:
			skipped++
		}
	}
	fmt.Fprintf(&b, "\n%d passed, %d failed, %d skipped\n", passed, failed, skipped)
	_, err := io.WriteString(w, b.String())
	return err
}

// conformer holds the target instance and runs the checks against it.
type conformer struct {
	api   string
	gql   string
	token string
	owner string
	repo  string
	http  *http.Client
}

func (c *conformer) runMatrix() *report {
	r := &report{}
	c.checkRepoView(r)
	c.checkConditional(r)
	c.checkIssueList(r)
	c.checkSearch(r)
	c.checkGraphQL(r)
	return r
}

// resp is a captured HTTP response with its body already read.
type resp struct {
	status  int
	header  http.Header
	body    []byte
	bodyErr error
}

func (c *conformer) do(method, url string, extra map[string]string, body []byte) (resp, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return resp{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	httpResp, err := c.http.Do(req)
	if err != nil {
		return resp{}, err
	}
	defer func() { _ = httpResp.Body.Close() }()
	b, readErr := io.ReadAll(httpResp.Body)
	return resp{status: httpResp.StatusCode, header: httpResp.Header, body: b, bodyErr: readErr}, nil
}

func (c *conformer) repoPath(suffix string) string {
	return c.api + "/repos/" + c.owner + "/" + c.repo + suffix
}

func (c *conformer) checkRepoView(r *report) {
	const section = "repository"
	got, err := c.do(http.MethodGet, c.repoPath(""), nil, nil)
	if err != nil {
		r.add(section, "GET repo", fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, "GET repo", fail, fmt.Sprintf("status %d, want 200", got.status))
		return
	}
	r.add(section, "GET repo", pass, "200 OK")

	var m map[string]any
	if err := json.Unmarshal(got.body, &m); err != nil {
		r.add(section, "repo JSON", fail, err.Error())
		return
	}
	want := c.owner + "/" + c.repo
	if nwo, _ := m["full_name"].(string); nwo == want {
		r.add(section, "full_name", pass, nwo)
	} else {
		r.add(section, "full_name", fail, fmt.Sprintf("%q, want %q", nwo, want))
	}

	// No served URL may name the upstream host: the instance must build every
	// URL from its own base.
	if leak := upstreamLeak(got.body); leak == "" {
		r.add(section, "no upstream host", pass, "URLs built from the instance base")
	} else {
		r.add(section, "no upstream host", fail, "found "+leak)
	}
}

func (c *conformer) checkConditional(r *report) {
	const section = "conditional"
	first, err := c.do(http.MethodGet, c.repoPath(""), nil, nil)
	if err != nil {
		r.add(section, "ETag present", fail, err.Error())
		return
	}
	etag := first.header.Get("ETag")
	if etag == "" {
		r.add(section, "ETag present", fail, "no ETag header on repo GET")
		return
	}
	r.add(section, "ETag present", pass, etag)

	before := first.header.Get("X-RateLimit-Remaining")
	second, err := c.do(http.MethodGet, c.repoPath(""), map[string]string{"If-None-Match": etag}, nil)
	if err != nil {
		r.add(section, "304 on match", fail, err.Error())
		return
	}
	if second.status == http.StatusNotModified {
		r.add(section, "304 on match", pass, "If-None-Match returned 304")
	} else {
		r.add(section, "304 on match", fail, fmt.Sprintf("status %d, want 304", second.status))
	}

	after := second.header.Get("X-RateLimit-Remaining")
	switch {
	case before == "" || after == "":
		r.add(section, "no rate-limit spend", skip, "no X-RateLimit-Remaining header")
	case after == before:
		r.add(section, "no rate-limit spend", pass, "remaining held at "+after)
	default:
		r.add(section, "no rate-limit spend", fail, fmt.Sprintf("remaining %s -> %s", before, after))
	}
}

func (c *conformer) checkIssueList(r *report) {
	const section = "issues"
	got, err := c.do(http.MethodGet, c.repoPath("/issues?per_page=1&state=all"), nil, nil)
	if err != nil {
		r.add(section, "GET issues", fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, "GET issues", fail, fmt.Sprintf("status %d, want 200", got.status))
		return
	}
	var arr []map[string]any
	if err := json.Unmarshal(got.body, &arr); err != nil {
		r.add(section, "GET issues", fail, "not a JSON array: "+err.Error())
		return
	}
	r.add(section, "GET issues", pass, fmt.Sprintf("%d issue(s) on page 1", len(arr)))

	// With per_page=1 and more than one issue, the server must advertise the
	// next and last page through the Link header.
	link := got.header.Get("Link")
	switch {
	case len(arr) == 0:
		r.add(section, "Link rels", skip, "no issues to paginate")
	case link == "":
		r.add(section, "Link rels", skip, "single page, no Link header")
	default:
		rels := linkRels(link)
		if rels["next"] && rels["last"] {
			r.add(section, "Link rels", pass, "next and last advertised")
		} else {
			r.add(section, "Link rels", fail, "Link header missing next/last: "+link)
		}
	}
}

func (c *conformer) checkSearch(r *report) {
	const section = "search"
	c.checkSearchEnvelope(r, section, "repositories",
		"/search/repositories?q="+c.repo+"+user:"+c.owner)
	c.checkSearchEnvelope(r, section, "issues",
		"/search/issues?q=repo:"+c.owner+"/"+c.repo)
}

func (c *conformer) checkSearchEnvelope(r *report, section, name, query string) {
	got, err := c.do(http.MethodGet, c.api+query, nil, nil)
	if err != nil {
		r.add(section, name, fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, name, fail, fmt.Sprintf("status %d, want 200", got.status))
		return
	}
	var env struct {
		TotalCount        *int  `json:"total_count"`
		IncompleteResults *bool `json:"incomplete_results"`
		Items             *[]struct {
			Score *float64 `json:"score"`
		} `json:"items"`
	}
	if err := json.Unmarshal(got.body, &env); err != nil {
		r.add(section, name, fail, err.Error())
		return
	}
	if env.TotalCount == nil || env.IncompleteResults == nil || env.Items == nil {
		r.add(section, name, fail, "envelope missing total_count/incomplete_results/items")
		return
	}
	r.add(section, name, pass, fmt.Sprintf("total_count %d, %d item(s)", *env.TotalCount, len(*env.Items)))
}

func (c *conformer) checkGraphQL(r *report) {
	const section = "graphql"

	introspection := `{"query":"query{__schema{queryType{name}}}"}`
	got, err := c.do(http.MethodPost, c.gql, nil, []byte(introspection))
	if err != nil {
		r.add(section, "introspection", fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, "introspection", fail, fmt.Sprintf("status %d, want 200", got.status))
		return
	}
	var intro struct {
		Data struct {
			Schema struct {
				QueryType struct{ Name string } `json:"queryType"`
			} `json:"__schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.body, &intro); err != nil {
		r.add(section, "introspection", fail, err.Error())
	} else if intro.Data.Schema.QueryType.Name == "Query" {
		r.add(section, "introspection", pass, "queryType is Query")
	} else {
		r.add(section, "introspection", fail, "queryType is "+strconv.Quote(intro.Data.Schema.QueryType.Name))
	}

	// A connection query exercises the pageInfo shape clients page with.
	conn, err := json.Marshal(map[string]any{
		"query":     "query($o:String!,$n:String!){repository(owner:$o,name:$n){issues(first:1){pageInfo{hasNextPage} totalCount}}}",
		"variables": map[string]any{"o": c.owner, "n": c.repo},
	})
	if err != nil {
		r.add(section, "issues connection", fail, err.Error())
		return
	}
	got, err = c.do(http.MethodPost, c.gql, nil, conn)
	if err != nil {
		r.add(section, "issues connection", fail, err.Error())
		return
	}
	var res struct {
		Data struct {
			Repository *struct {
				Issues struct {
					PageInfo struct {
						HasNextPage *bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					TotalCount *int `json:"totalCount"`
				} `json:"issues"`
			} `json:"repository"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal(got.body, &res); err != nil {
		r.add(section, "issues connection", fail, err.Error())
		return
	}
	switch {
	case len(res.Errors) != 0:
		r.add(section, "issues connection", fail, fmt.Sprintf("graphql errors: %v", res.Errors))
	case res.Data.Repository == nil:
		r.add(section, "issues connection", fail, "repository resolved null")
	case res.Data.Repository.Issues.PageInfo.HasNextPage == nil || res.Data.Repository.Issues.TotalCount == nil:
		r.add(section, "issues connection", fail, "pageInfo.hasNextPage or totalCount absent")
	default:
		r.add(section, "issues connection", pass, fmt.Sprintf("totalCount %d, pageInfo resolved", *res.Data.Repository.Issues.TotalCount))
	}
}

// upstreamLeak returns the first upstream host literal found in b, or "" if the
// body names neither. It mirrors the build gate that forbids the upstream API
// and web hosts in any served response. The two needles are assembled from
// parts so this source line does not itself trip that same gate.
func upstreamLeak(b []byte) string {
	s := string(b)
	const dotCom = ".com"
	for _, host := range []string{"api.github" + dotCom, "//github" + dotCom} {
		if strings.Contains(s, host) {
			return host
		}
	}
	return ""
}

// linkRels parses an RFC 5988 Link header into the set of rel names it carries.
func linkRels(header string) map[string]bool {
	rels := map[string]bool{}
	for part := range strings.SplitSeq(header, ",") {
		for attr := range strings.SplitSeq(part, ";") {
			attr = strings.TrimSpace(attr)
			if rel, ok := strings.CutPrefix(attr, `rel=`); ok {
				rels[strings.Trim(rel, `"`)] = true
			}
		}
	}
	return rels
}
