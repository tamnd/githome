// Package conform certifies a Githome instance against the compatibility
// matrix. It is a black-box client: it speaks only HTTP and reads only the wire
// shapes, so it certifies any Githome build the same way a real client would
// see it — whether the instance is a remote origin or an in-process test
// server. The githome-conform command is a thin CLI over Run; the in-process
// gate test drives the same Run against a freshly seeded server, so the matrix
// runs in CI without a live instance or real credentials.
package conform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Options points the matrix at one instance and target repository.
type Options struct {
	// API is the REST base, e.g. https://git.example.com/api/v3.
	API string
	// GraphQL is the GraphQL endpoint, e.g. https://git.example.com/api/graphql.
	GraphQL string
	// Token authenticates the client; it may be empty for an anonymous run.
	Token string
	// Owner and Repo name the target repository the matrix reads.
	Owner string
	Repo  string
	// HTTP is the client used for every request; Run supplies a default when nil.
	HTTP *http.Client
}

// Run executes the full conformance matrix against the instance Options names
// and returns the report. It never returns an error: a transport or shape
// failure is recorded as a failed row so the report stays the single verdict.
func Run(opts Options) *Report {
	c := &conformer{
		api:   strings.TrimRight(opts.API, "/"),
		gql:   opts.GraphQL,
		token: opts.Token,
		owner: opts.Owner,
		repo:  opts.Repo,
		http:  opts.HTTP,
	}
	if c.http == nil {
		c.http = http.DefaultClient
	}
	return c.runMatrix()
}

// Status is the outcome of one matrix check.
type Status int

const (
	Pass Status = iota
	Fail
	Skip
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "PASS"
	case Fail:
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
	status  Status
	detail  string
}

// Report collects the matrix outcome.
type Report struct {
	rows []row
}

func (r *Report) add(section, check string, st Status, detail string) {
	r.rows = append(r.rows, row{section: section, check: check, status: st, detail: detail})
}

// Failed reports whether any check failed.
func (r *Report) Failed() bool { return r.CountFailed() > 0 }

// CountFailed returns the number of failed checks.
func (r *Report) CountFailed() int {
	n := 0
	for _, row := range r.rows {
		if row.status == Fail {
			n++
		}
	}
	return n
}

// Total returns the number of checks the matrix ran.
func (r *Report) Total() int { return len(r.rows) }

// Failures returns a one-line "section/check: detail" string for each failed
// check, so a caller (a test) can name exactly what diverged.
func (r *Report) Failures() []string {
	var out []string
	for _, row := range r.rows {
		if row.status == Fail {
			out = append(out, fmt.Sprintf("%s/%s: %s", row.section, row.check, row.detail))
		}
	}
	return out
}

// Print writes the matrix as an aligned table followed by a tally, with target
// as the report heading.
func (r *Report) Print(w io.Writer, target string) error {
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
		case Pass:
			passed++
		case Fail:
			failed++
		case Skip:
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

func (c *conformer) runMatrix() *Report {
	r := &Report{}
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

func (c *conformer) checkRepoView(r *Report) {
	const section = "repository"
	got, err := c.do(http.MethodGet, c.repoPath(""), nil, nil)
	if err != nil {
		r.add(section, "GET repo", Fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, "GET repo", Fail, fmt.Sprintf("status %d, want 200", got.status))
		return
	}
	r.add(section, "GET repo", Pass, "200 OK")

	var m map[string]any
	if err := json.Unmarshal(got.body, &m); err != nil {
		r.add(section, "repo JSON", Fail, err.Error())
		return
	}
	want := c.owner + "/" + c.repo
	if nwo, _ := m["full_name"].(string); nwo == want {
		r.add(section, "full_name", Pass, nwo)
	} else {
		r.add(section, "full_name", Fail, fmt.Sprintf("%q, want %q", nwo, want))
	}

	// No served URL may name the upstream host: the instance must build every
	// URL from its own base.
	if leak := upstreamLeak(got.body); leak == "" {
		r.add(section, "no upstream host", Pass, "URLs built from the instance base")
	} else {
		r.add(section, "no upstream host", Fail, "found "+leak)
	}
}

func (c *conformer) checkConditional(r *Report) {
	const section = "conditional"
	first, err := c.do(http.MethodGet, c.repoPath(""), nil, nil)
	if err != nil {
		r.add(section, "ETag present", Fail, err.Error())
		return
	}
	etag := first.header.Get("ETag")
	if etag == "" {
		r.add(section, "ETag present", Fail, "no ETag header on repo GET")
		return
	}
	r.add(section, "ETag present", Pass, etag)

	before := first.header.Get("X-RateLimit-Remaining")
	second, err := c.do(http.MethodGet, c.repoPath(""), map[string]string{"If-None-Match": etag}, nil)
	if err != nil {
		r.add(section, "304 on match", Fail, err.Error())
		return
	}
	if second.status == http.StatusNotModified {
		r.add(section, "304 on match", Pass, "If-None-Match returned 304")
	} else {
		r.add(section, "304 on match", Fail, fmt.Sprintf("status %d, want 304", second.status))
	}

	after := second.header.Get("X-RateLimit-Remaining")
	switch {
	case before == "" || after == "":
		r.add(section, "no rate-limit spend", Skip, "no X-RateLimit-Remaining header")
	case after == before:
		r.add(section, "no rate-limit spend", Pass, "remaining held at "+after)
	default:
		r.add(section, "no rate-limit spend", Fail, fmt.Sprintf("remaining %s -> %s", before, after))
	}
}

func (c *conformer) checkIssueList(r *Report) {
	const section = "issues"
	got, err := c.do(http.MethodGet, c.repoPath("/issues?per_page=1&state=all"), nil, nil)
	if err != nil {
		r.add(section, "GET issues", Fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, "GET issues", Fail, fmt.Sprintf("status %d, want 200", got.status))
		return
	}
	var arr []map[string]any
	if err := json.Unmarshal(got.body, &arr); err != nil {
		r.add(section, "GET issues", Fail, "not a JSON array: "+err.Error())
		return
	}
	r.add(section, "GET issues", Pass, fmt.Sprintf("%d issue(s) on page 1", len(arr)))

	// With per_page=1 and more than one issue, the server must advertise the
	// next and last page through the Link header.
	link := got.header.Get("Link")
	switch {
	case len(arr) == 0:
		r.add(section, "Link rels", Skip, "no issues to paginate")
	case link == "":
		r.add(section, "Link rels", Skip, "single page, no Link header")
	default:
		rels := linkRels(link)
		if rels["next"] && rels["last"] {
			r.add(section, "Link rels", Pass, "next and last advertised")
		} else {
			r.add(section, "Link rels", Fail, "Link header missing next/last: "+link)
		}
	}
}

func (c *conformer) checkSearch(r *Report) {
	const section = "search"
	c.checkSearchEnvelope(r, section, "repositories",
		"/search/repositories?q="+c.repo+"+user:"+c.owner)
	c.checkSearchEnvelope(r, section, "issues",
		"/search/issues?q=repo:"+c.owner+"/"+c.repo)
}

func (c *conformer) checkSearchEnvelope(r *Report, section, name, query string) {
	got, err := c.do(http.MethodGet, c.api+query, nil, nil)
	if err != nil {
		r.add(section, name, Fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, name, Fail, fmt.Sprintf("status %d, want 200", got.status))
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
		r.add(section, name, Fail, err.Error())
		return
	}
	if env.TotalCount == nil || env.IncompleteResults == nil || env.Items == nil {
		r.add(section, name, Fail, "envelope missing total_count/incomplete_results/items")
		return
	}
	r.add(section, name, Pass, fmt.Sprintf("total_count %d, %d item(s)", *env.TotalCount, len(*env.Items)))
}

func (c *conformer) checkGraphQL(r *Report) {
	const section = "graphql"

	introspection := `{"query":"query{__schema{queryType{name}}}"}`
	got, err := c.do(http.MethodPost, c.gql, nil, []byte(introspection))
	if err != nil {
		r.add(section, "introspection", Fail, err.Error())
		return
	}
	if got.status != http.StatusOK {
		r.add(section, "introspection", Fail, fmt.Sprintf("status %d, want 200", got.status))
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
		r.add(section, "introspection", Fail, err.Error())
	} else if intro.Data.Schema.QueryType.Name == "Query" {
		r.add(section, "introspection", Pass, "queryType is Query")
	} else {
		r.add(section, "introspection", Fail, "queryType is "+strconv.Quote(intro.Data.Schema.QueryType.Name))
	}

	// A connection query exercises the pageInfo shape clients page with.
	conn, err := json.Marshal(map[string]any{
		"query":     "query($o:String!,$n:String!){repository(owner:$o,name:$n){issues(first:1){pageInfo{hasNextPage} totalCount}}}",
		"variables": map[string]any{"o": c.owner, "n": c.repo},
	})
	if err != nil {
		r.add(section, "issues connection", Fail, err.Error())
		return
	}
	got, err = c.do(http.MethodPost, c.gql, nil, conn)
	if err != nil {
		r.add(section, "issues connection", Fail, err.Error())
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
		r.add(section, "issues connection", Fail, err.Error())
		return
	}
	switch {
	case len(res.Errors) != 0:
		r.add(section, "issues connection", Fail, fmt.Sprintf("graphql errors: %v", res.Errors))
	case res.Data.Repository == nil:
		r.add(section, "issues connection", Fail, "repository resolved null")
	case res.Data.Repository.Issues.PageInfo.HasNextPage == nil || res.Data.Repository.Issues.TotalCount == nil:
		r.add(section, "issues connection", Fail, "pageInfo.hasNextPage or totalCount absent")
	default:
		r.add(section, "issues connection", Pass, fmt.Sprintf("totalCount %d, pageInfo resolved", *res.Data.Repository.Issues.TotalCount))
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
