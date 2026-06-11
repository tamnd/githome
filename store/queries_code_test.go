package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestCodeSearchRoundTrip indexes documents for two repositories and confirms
// the FTS query matches on content, matches on path, scopes by repository, and
// counts the full match set. The same surface runs on both dialects.
func TestCodeSearchRoundTrip(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		a := seedRepo(t, st, "octocat", &store.RepoRow{Name: "alpha"})
		b := seedRepo(t, st, "hubot", &store.RepoRow{Name: "beta"})

		if err := st.ReplaceCodeDocs(ctx, a.PK, "aaa111", false, []store.CodeDoc{
			{Path: "README.md", SHA: "s1", Content: "# Hello world"},
			{Path: "main.go", SHA: "s2", Content: "package main\nfunc main() {}"},
			{Path: "assets/logo.png", SHA: "s3", Content: ""},
		}); err != nil {
			t.Fatalf("ReplaceCodeDocs alpha: %v", err)
		}
		if err := st.ReplaceCodeDocs(ctx, b.PK, "bbb222", false, []store.CodeDoc{
			{Path: "hello.txt", SHA: "s4", Content: "hello from beta"},
		}); err != nil {
			t.Fatalf("ReplaceCodeDocs beta: %v", err)
		}

		// Content match, scoped to one repository.
		hits, err := st.SearchCode(ctx, store.CodeSearch{RepoPKs: []int64{a.PK}, Terms: []string{"hello"}})
		if err != nil {
			t.Fatalf("SearchCode: %v", err)
		}
		if len(hits) != 1 || hits[0].Path != "README.md" || hits[0].SHA != "s1" {
			t.Fatalf("content match = %+v, want README.md/s1", hits)
		}

		// The same term across both repositories.
		both, err := st.SearchCode(ctx, store.CodeSearch{RepoPKs: []int64{a.PK, b.PK}, Terms: []string{"hello"}})
		if err != nil {
			t.Fatalf("SearchCode both: %v", err)
		}
		if len(both) != 2 {
			t.Fatalf("cross-repo match = %d hits, want 2", len(both))
		}
		if n, _ := st.CountSearchCode(ctx, store.CodeSearch{RepoPKs: []int64{a.PK, b.PK}, Terms: []string{"hello"}}); n != 2 {
			t.Errorf("CountSearchCode = %d, want 2", n)
		}

		// Path-only documents (binary files index with empty content) still
		// answer path queries.
		byPath, err := st.SearchCode(ctx, store.CodeSearch{RepoPKs: []int64{a.PK}, Terms: []string{"logo"}})
		if err != nil {
			t.Fatalf("SearchCode path: %v", err)
		}
		if len(byPath) != 1 || byPath[0].Path != "assets/logo.png" {
			t.Fatalf("path match = %+v, want assets/logo.png", byPath)
		}

		// No terms lists every indexed file in scope, ordered by path.
		all, err := st.SearchCode(ctx, store.CodeSearch{RepoPKs: []int64{a.PK}})
		if err != nil {
			t.Fatalf("SearchCode no terms: %v", err)
		}
		if len(all) != 3 || all[0].Path != "README.md" || all[2].Path != "main.go" {
			t.Fatalf("unfiltered listing = %+v, want 3 entries ordered by path", all)
		}
	})
}

// TestReplaceCodeDocsSwapsWholesale confirms a reindex removes documents that
// left the tree and updates the recorded head sha.
func TestReplaceCodeDocsSwapsWholesale(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		r := seedRepo(t, st, "octocat", &store.RepoRow{Name: "swap"})

		if _, err := st.CodeIndexHead(ctx, r.PK); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("CodeIndexHead before indexing = %v, want ErrNotFound", err)
		}

		if err := st.ReplaceCodeDocs(ctx, r.PK, "old111", false, []store.CodeDoc{
			{Path: "gone.txt", SHA: "s1", Content: "stale needle"},
		}); err != nil {
			t.Fatalf("ReplaceCodeDocs first: %v", err)
		}
		if err := st.ReplaceCodeDocs(ctx, r.PK, "new222", true, []store.CodeDoc{
			{Path: "kept.txt", SHA: "s2", Content: "fresh needle"},
		}); err != nil {
			t.Fatalf("ReplaceCodeDocs second: %v", err)
		}

		head, err := st.CodeIndexHead(ctx, r.PK)
		if err != nil {
			t.Fatalf("CodeIndexHead: %v", err)
		}
		if head != "new222" {
			t.Fatalf("CodeIndexHead = %q, want new222", head)
		}
		hits, err := st.SearchCode(ctx, store.CodeSearch{RepoPKs: []int64{r.PK}, Terms: []string{"needle"}})
		if err != nil {
			t.Fatalf("SearchCode: %v", err)
		}
		if len(hits) != 1 || hits[0].Path != "kept.txt" {
			t.Fatalf("post-swap match = %+v, want only kept.txt", hits)
		}

		trunc, err := st.CodeIndexTruncated(ctx, []int64{r.PK})
		if err != nil {
			t.Fatalf("CodeIndexTruncated: %v", err)
		}
		if !trunc {
			t.Fatal("CodeIndexTruncated = false, want true after a truncated reindex")
		}
	})
}
