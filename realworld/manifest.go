package realworld

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ManifestSchema is the manifest format version, bumped when the manifest shape
// changes so an old reader refuses a corpus it cannot interpret.
const ManifestSchema = 1

// Manifest pins a corpus and records what was measured and what was synthesized,
// so a corpus is reproducible and no reader mistakes a modeled value for a real
// one. It is the single file that freezes the corpus: the dataset revision and
// the per-repo git pins are its OFFICIAL anchors, the reactor pool and any
// pseudonymization are its MODELED notes, and Measured holds the row counts the
// seeder actually wrote rather than any count asserted up front.
type Manifest struct {
	Schema int `json:"schema"`
	// Note is a human description of this corpus build; it is not load-bearing.
	Note string `json:"note,omitempty"`
	// DatasetRevision pins the metadata source (the dataset repo commit, or the
	// GraphQL export run id). It is the metadata analog of the per-repo SHA.
	DatasetRevision string `json:"dataset_revision"`
	// FixtureTier names the tier this corpus serves: rw-smoke, rw-meta,
	// rw-write, rw-git, or rw-full. Tiers bound how much a CI leg loads.
	FixtureTier string `json:"fixture_tier"`
	// Pseudonymized is true when logins and bodies were run through the
	// pseudonymizer, so the corpus carries no real identities.
	Pseudonymized bool `json:"pseudonymized"`
	// Reactor records the bounded synthetic reactor pool the seeder materializes
	// reaction counts against; reactions are the one MODELED count in a corpus.
	Reactor ReactorPool `json:"reactor"`
	// Repos is one entry per repository in this corpus.
	Repos []RepoManifest `json:"repos"`
	// SeederVersion and SchemaVersion pin the tooling and the store schema the
	// corpus was built against, the rest of the reproducibility checklist.
	SeederVersion string `json:"seeder_version,omitempty"`
	SchemaVersion int    `json:"schema_version,omitempty"`
	// Dropped records anything this build bounded or skipped — a truncated
	// table, an unreachable source, a sampled range — so a partial corpus never
	// reads as a complete one.
	Dropped []DropNote `json:"dropped,omitempty"`
}

// ReactorPool is the synthetic identity pool reaction counts are materialized
// against. Size bounds how many reactor users exist; Seed fixes the assignment
// so two builds produce the same rows.
type ReactorPool struct {
	Size int   `json:"size"`
	Seed int64 `json:"seed"`
}

// RepoManifest pins one repository's git side and records the measured row
// counts of its metadata, with the provenance of the whole entry.
type RepoManifest struct {
	Repo       RepoRef        `json:"repo"`
	Provenance Provenance     `json:"provenance"`
	Rows       map[string]int `json:"rows,omitempty"`
	// GitBytes and TreeEntries are the measured git artifact; zero until a
	// mirror is cloned and measured.
	GitBytes    int64 `json:"git_bytes,omitempty"`
	TreeEntries int   `json:"tree_entries,omitempty"`
}

// DropNote records one bounded or skipped piece of a corpus build, with the
// reason, so coverage is never silently capped.
type DropNote struct {
	What   string `json:"what"`
	Count  int    `json:"count,omitempty"`
	Reason string `json:"reason"`
}

// DefaultReactorPool is the reactor pool a corpus uses unless a manifest
// overrides it: 200 synthetic reactors, a fixed assignment seed.
var DefaultReactorPool = ReactorPool{Size: 200, Seed: 0x6e7e}

// NewManifest builds a manifest for a tier with the default reactor pool and the
// current schema version, ready for the seeder to fill Measured into.
func NewManifest(tier, datasetRevision string) *Manifest {
	return &Manifest{
		Schema:          ManifestSchema,
		DatasetRevision: datasetRevision,
		FixtureTier:     tier,
		Reactor:         DefaultReactorPool,
		SchemaVersion:   1,
	}
}

// RepoNames returns the owner/name of every repo in the manifest, sorted, for
// stable logging.
func (m *Manifest) RepoNames() []string {
	out := make([]string, 0, len(m.Repos))
	for _, r := range m.Repos {
		out = append(out, r.Repo.NWO())
	}
	sort.Strings(out)
	return out
}

// Drop records a bounded or skipped piece of the build.
func (m *Manifest) Drop(what, reason string, count int) {
	m.Dropped = append(m.Dropped, DropNote{What: what, Count: count, Reason: reason})
}

// LoadManifest reads and validates a manifest from disk.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("realworld: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("realworld: parse manifest %s: %w", path, err)
	}
	if m.Schema != ManifestSchema {
		return nil, fmt.Errorf("realworld: manifest %s is schema %d, this build reads schema %d", path, m.Schema, ManifestSchema)
	}
	return &m, nil
}

// Save writes the manifest as indented JSON.
func (m *Manifest) Save(path string) error {
	if m.Schema == 0 {
		m.Schema = ManifestSchema
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("realworld: write manifest: %w", err)
	}
	return nil
}
