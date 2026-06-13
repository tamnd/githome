// Command githome-conform certifies a running Githome instance against the
// compatibility matrix: it points at a base URL with an auth token and a target
// repository, runs the section-3 surface (repository read, conditional
// requests, issue listing and pagination, the search endpoints, and GraphQL
// introspection plus a connection query), and prints a pass/fail report.
//
// It is a black-box client: it speaks only HTTP and reads only the wire shapes,
// so it certifies any Githome build the same way a real client would see it. It
// exits non-zero when any check fails, so it can gate a release. The matrix
// itself lives in package conform, which the in-process gate test drives the
// same way against a freshly seeded server, so the suite runs in CI too.
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
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tamnd/githome/conform"
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

	rep := conform.Run(conform.Options{
		API:     apiBase,
		GraphQL: gqlURL,
		Token:   *token,
		Owner:   owner,
		Repo:    repo,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	})
	if err := rep.Print(out, fmt.Sprintf("%s/%s @ %s", owner, repo, origin)); err != nil {
		return err
	}
	if rep.Failed() {
		return fmt.Errorf("%d of %d checks failed", rep.CountFailed(), rep.Total())
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
