package realworld

import (
	"context"
	"errors"
	"fmt"
)

// Stage A turns a live source into a normalized Corpus. The work splits three
// ways by source — a git mirror for history, the public GraphQL API for the bulk
// of the metadata, the GH Archive event stream for the dense timeline that is
// cheaper to reconstruct from the public log than to pull per issue — but every
// path produces the same Corpus, so Stage B never knows which source a row came
// from. An Exporter is one source's contribution.
//
// Only the fixture exporter runs without network. The GraphQL and Archive
// exporters need network and credentials; they are represented here by their
// configuration and their plan so the surrounding orchestration, budgeting, and
// checkpointing are real and testable, and execution is gated behind an
// explicit, credentialed call rather than faked.

// ErrRequiresNetwork is returned by an exporter whose source needs network or
// credentials that are not configured, so a caller in an offline or unit-test
// environment gets a clear signal rather than a silent empty corpus.
var ErrRequiresNetwork = errors.New("realworld: this export source requires network access and credentials")

// Exporter produces the metadata corpus for one repository from one source.
type Exporter interface {
	// Export returns the corpus for ref, or ErrRequiresNetwork when the source
	// is not reachable in this environment.
	Export(ctx context.Context, ref RepoRef) (*Corpus, error)
	// Source names the exporter for logs and the manifest provenance.
	Source() string
}

// FixtureExporter reads a corpus back from a snapshot directory. It is the
// offline source: a previously exported snapshot, or a small checked-in fixture,
// re-read as if freshly exported. It is the exporter the tests and the seed-only
// CLI path use.
type FixtureExporter struct {
	Root string
}

// Export reads ref's corpus from the snapshot root.
func (e FixtureExporter) Export(_ context.Context, ref RepoRef) (*Corpus, error) {
	return ReadCorpus(e.Root, ref)
}

// Source identifies the fixture exporter.
func (e FixtureExporter) Source() string { return "fixture:" + e.Root }

// RateBudget bounds how many API points an export run may spend, the way the
// GraphQL API meters cost in points rather than requests. It is honest about
// exhaustion: once the budget is spent, Spend refuses further work so an export
// stops and checkpoints rather than hammering a throttled endpoint.
type RateBudget struct {
	total int
	spent int
}

// NewRateBudget returns a budget of total points.
func NewRateBudget(total int) *RateBudget { return &RateBudget{total: total} }

// Spend charges n points, returning false when the budget cannot cover them.
// The caller checkpoints and stops on false rather than proceeding.
func (b *RateBudget) Spend(n int) bool {
	if b.total > 0 && b.spent+n > b.total {
		return false
	}
	b.spent += n
	return true
}

// Spent reports how many points have been charged.
func (b *RateBudget) Spent() int { return b.spent }

// Remaining reports how many points are left, or a large number when the budget
// is unbounded (total <= 0).
func (b *RateBudget) Remaining() int {
	if b.total <= 0 {
		return 1 << 30
	}
	return b.total - b.spent
}

// Checkpoint is the resumable-export journal: it records which repo/table pairs
// an export run has finished, so a run interrupted by rate-limit exhaustion or a
// crash resumes at the first unfinished pair instead of re-exporting from the
// top. It is a plain value the caller persists alongside the snapshot.
type Checkpoint struct {
	Done map[string]bool `json:"done"`
}

// NewCheckpoint returns an empty journal.
func NewCheckpoint() *Checkpoint { return &Checkpoint{Done: map[string]bool{}} }

// key is the journal key for one repo/table pair.
func ckptKey(ref RepoRef, table string) string { return ref.NWO() + "#" + table }

// IsDone reports whether ref's table has already been exported.
func (c *Checkpoint) IsDone(ref RepoRef, table string) bool {
	return c.Done[ckptKey(ref, table)]
}

// Mark records ref's table as exported.
func (c *Checkpoint) Mark(ref RepoRef, table string) {
	if c.Done == nil {
		c.Done = map[string]bool{}
	}
	c.Done[ckptKey(ref, table)] = true
}

// GitMirrorPlan is the recipe to mirror a repository's history into a git store,
// expressed as the commands to run rather than run inline, so the plan is
// testable and the network/disk-heavy execution is an explicit, separate step.
// The maintenance pass (repack, bitmap, commit-graph, multi-pack-index) is what
// makes a freshly cloned giant serve cold reads at the same speed a warmed
// long-lived repository does.
type GitMirrorPlan struct {
	Ref       RepoRef
	MirrorURL string
	PinnedSHA string
}

// Commands returns the git invocations the plan runs, in order: a bare mirror
// clone, a reset of the advertised tip to the pin so a fetch benchmark has real
// new commits to deliver, and the maintenance pass. dest is the bare repo path
// in the git store.
func (p GitMirrorPlan) Commands(dest string) [][]string {
	url := p.MirrorURL
	if url == "" {
		url = "https://github.com/" + p.Ref.NWO() + ".git"
	}
	cmds := [][]string{
		{"git", "clone", "--mirror", url, dest},
		{"git", "-C", dest, "repack", "-adb"},
		{"git", "-C", dest, "commit-graph", "write", "--reachable", "--changed-paths"},
		{"git", "-C", dest, "multi-pack-index", "write"},
	}
	if p.PinnedSHA != "" {
		// Point the advertised default branch at the pin; the mirror keeps the
		// history past it for the fetch to deliver.
		cmds = append(cmds, []string{"git", "-C", dest, "update-ref",
			"refs/heads/" + p.Ref.branch(), p.PinnedSHA})
	}
	return cmds
}

func (r RepoRef) branch() string {
	if r.DefaultBranch != "" {
		return r.DefaultBranch
	}
	return "main"
}

// ExportToSnapshot runs an exporter over every repo in refs, writes each corpus
// into the snapshot root, and fills the manifest with the measured row counts
// and the provenance. A repo whose source is unreachable (ErrRequiresNetwork) is
// recorded as a drop rather than failing the whole run, so an offline build
// produces an honest partial snapshot the manifest names as partial. The
// manifest the caller passes is updated in place and saved at the root.
func ExportToSnapshot(ctx context.Context, ex Exporter, refs []RepoRef, m *Manifest, root string) error {
	for _, ref := range refs {
		c, err := ex.Export(ctx, ref)
		if errors.Is(err, ErrRequiresNetwork) {
			m.Drop(ref.NWO(), "source unreachable in this environment ("+ex.Source()+")", 0)
			continue
		}
		if err != nil {
			return fmt.Errorf("realworld: export %s: %w", ref.NWO(), err)
		}
		if err := WriteCorpus(root, c); err != nil {
			return err
		}
		m.Repos = append(m.Repos, RepoManifest{
			Repo:       c.Repo,
			Provenance: Official,
			Rows:       c.rowCounts(),
		})
	}
	return m.Save(snapshotManifestPath(root))
}

// rowCounts is the measured per-table size of a corpus, written into the
// manifest so the recorded numbers are the ones actually exported.
func (c *Corpus) rowCounts() map[string]int {
	return map[string]int{
		"issues":          len(c.Issues),
		"pull_requests":   len(c.PullRequests),
		"comments":        len(c.Comments),
		"reviews":         len(c.Reviews),
		"review_comments": len(c.ReviewComments),
		"timeline_events": len(c.TimelineEvents),
		"pr_files":        len(c.PRFiles),
		"commit_statuses": len(c.CommitStatuses),
	}
}
