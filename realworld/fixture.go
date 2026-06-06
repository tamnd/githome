package realworld

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The snapshot is the on-disk form of a Corpus: one directory per corpus, with
// a manifest at the root and, under repo/<owner>/<name>/, one JSON-lines file
// per table. JSON Lines (one object per line) is chosen over Parquet for the
// checked-in fixtures because it is diffable, needs no external reader, and
// streams a line at a time, which is what the seeder wants. A production export
// of a multi-hundred-thousand-row repo would write Parquet for size; the format
// boundary is this file, so swapping the encoding later does not touch the
// seeder or the corpus model.

const (
	fileIssues         = "issues.jsonl"
	filePulls          = "pull_requests.jsonl"
	fileComments       = "comments.jsonl"
	fileReviews        = "reviews.jsonl"
	fileReviewComments = "review_comments.jsonl"
	fileTimeline       = "timeline_events.jsonl"
	filePRFiles        = "pr_files.jsonl"
	fileStatuses       = "commit_statuses.jsonl"
	fileRepo           = "repo.json"
)

// ManifestName is the manifest filename at the root of a snapshot directory.
const ManifestName = "realworld-manifest.json"

// snapshotManifestPath is the manifest path at the root of a snapshot.
func snapshotManifestPath(root string) string { return filepath.Join(root, ManifestName) }

// repoDir is the per-repo subdirectory of a snapshot, kept under a repo/ prefix
// so the manifest and any future top-level artifact never collide with an owner
// named "repo".
func repoDir(root string, ref RepoRef) string {
	return filepath.Join(root, "repo", ref.Owner, ref.Name)
}

// WriteCorpus writes one corpus into a snapshot directory, creating the per-repo
// layout. It does not write the manifest; the caller owns the manifest because
// it spans every repo in the snapshot.
func WriteCorpus(root string, c *Corpus) error {
	dir := repoDir(root, c.Repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, fileRepo), c.Repo); err != nil {
		return err
	}
	for _, w := range []struct {
		name string
		fn   func(string) error
	}{
		{fileIssues, func(p string) error { return writeJSONL(p, c.Issues) }},
		{filePulls, func(p string) error { return writeJSONL(p, c.PullRequests) }},
		{fileComments, func(p string) error { return writeJSONL(p, c.Comments) }},
		{fileReviews, func(p string) error { return writeJSONL(p, c.Reviews) }},
		{fileReviewComments, func(p string) error { return writeJSONL(p, c.ReviewComments) }},
		{fileTimeline, func(p string) error { return writeJSONL(p, c.TimelineEvents) }},
		{filePRFiles, func(p string) error { return writeJSONL(p, c.PRFiles) }},
		{fileStatuses, func(p string) error { return writeJSONL(p, c.CommitStatuses) }},
	} {
		if err := w.fn(filepath.Join(dir, w.name)); err != nil {
			return fmt.Errorf("realworld: write %s: %w", w.name, err)
		}
	}
	return nil
}

// ReadCorpus reads one repo's corpus back from a snapshot directory.
func ReadCorpus(root string, ref RepoRef) (*Corpus, error) {
	dir := repoDir(root, ref)
	c := &Corpus{Repo: ref}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("realworld: corpus for %s not found under %s: %w", ref.NWO(), root, err)
	}
	// The repo.json carries the pinned git side the manifest entry may not.
	if stored, err := readJSON[RepoRef](filepath.Join(dir, fileRepo)); err == nil {
		c.Repo = stored
	}
	var err error
	if c.Issues, err = readJSONL[Issue](filepath.Join(dir, fileIssues)); err != nil {
		return nil, err
	}
	if c.PullRequests, err = readJSONL[PullRequest](filepath.Join(dir, filePulls)); err != nil {
		return nil, err
	}
	if c.Comments, err = readJSONL[Comment](filepath.Join(dir, fileComments)); err != nil {
		return nil, err
	}
	if c.Reviews, err = readJSONL[Review](filepath.Join(dir, fileReviews)); err != nil {
		return nil, err
	}
	if c.ReviewComments, err = readJSONL[ReviewComment](filepath.Join(dir, fileReviewComments)); err != nil {
		return nil, err
	}
	if c.TimelineEvents, err = readJSONL[TimelineEvent](filepath.Join(dir, fileTimeline)); err != nil {
		return nil, err
	}
	if c.PRFiles, err = readJSONL[PRFile](filepath.Join(dir, filePRFiles)); err != nil {
		return nil, err
	}
	if c.CommitStatuses, err = readJSONL[CommitStatus](filepath.Join(dir, fileStatuses)); err != nil {
		return nil, err
	}
	return c, nil
}

// writeJSONL writes a slice as one JSON object per line. A nil slice writes an
// empty file, so a table that a repo has none of still has a file and the reader
// never has to special-case its absence from a missing-table error.
func writeJSONL[T any](path string, rows []T) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for i := range rows {
		if err := enc.Encode(rows[i]); err != nil {
			return err
		}
	}
	return w.Flush()
}

// readJSONL reads a JSON-lines file into a slice. A missing file reads as no
// rows, so a snapshot that predates a table still loads.
func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		var row T
		if err := json.Unmarshal(b, &row); err != nil {
			return nil, fmt.Errorf("realworld: %s line %d: %w", filepath.Base(path), line, err)
		}
		out = append(out, row)
	}
	return out, sc.Err()
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSON[T any](path string) (T, error) {
	var v T
	b, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(b, &v)
	return v, err
}
