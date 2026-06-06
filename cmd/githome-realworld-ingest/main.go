// Command githome-realworld-ingest runs the two-stage real-world corpus
// pipeline: Stage A exports public repository metadata and git history into a
// pinned, normalized snapshot, and Stage B seeds that snapshot into a target
// store. It is bench tooling, never imported by cmd/githome.
//
// Usage:
//
//	githome-realworld-ingest -stage seed   -data DIR -db DSN [-tier T] [-pseudonymize] [-manifest FILE]
//	githome-realworld-ingest -stage export -data DIR [-from DIR] [-tier T]
//	githome-realworld-ingest -stage all    -data DIR -db DSN -from DIR [-tier T] [-pseudonymize]
//
// Stage A against live github.com needs network and credentials, which this
// build does not carry, so -from points it at a local fixture snapshot to
// re-export offline. Stage B runs entirely locally against the snapshot in -data
// and the store in -db (or GITHOME_DATABASE_URL). The seeder writes the measured
// row counts back into the manifest so a run records what it actually loaded.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tamnd/githome/realworld"
	"github.com/tamnd/githome/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "githome-realworld-ingest:", err)
		os.Exit(1)
	}
}

type options struct {
	stage        string
	data         string
	from         string
	db           string
	git          string
	tier         string
	manifest     string
	pseudonymize bool
}

func run(args []string) error {
	fs := flag.NewFlagSet("githome-realworld-ingest", flag.ContinueOnError)
	var o options
	fs.StringVar(&o.stage, "stage", "seed", "pipeline stage: export, seed, or all")
	fs.StringVar(&o.data, "data", "", "snapshot directory (Stage A writes it, Stage B reads it)")
	fs.StringVar(&o.from, "from", "", "Stage A source: a local fixture snapshot to re-export offline (a live export needs network)")
	fs.StringVar(&o.db, "db", "", "target store DSN; defaults to GITHOME_DATABASE_URL")
	fs.StringVar(&o.git, "git", "", "git store path (reserved for the mirror stage, which needs network)")
	fs.StringVar(&o.tier, "tier", "rw-smoke", "fixture tier: rw-smoke, rw-meta, rw-write, rw-git, rw-full")
	fs.StringVar(&o.manifest, "manifest", "", "manifest path; defaults to <data>/"+realworld.ManifestName)
	fs.BoolVar(&o.pseudonymize, "pseudonymize", false, "rewrite logins to synthetic handles and redact bodies before seeding")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.data == "" {
		return errors.New("-data is required")
	}
	if o.manifest == "" {
		o.manifest = filepath.Join(o.data, realworld.ManifestName)
	}

	ctx := context.Background()
	switch o.stage {
	case "export":
		return doExport(ctx, o)
	case "seed":
		return doSeed(ctx, o)
	case "all":
		if err := doExport(ctx, o); err != nil {
			return err
		}
		return doSeed(ctx, o)
	default:
		return fmt.Errorf("unknown stage %q (want export|seed|all)", o.stage)
	}
}

// doExport runs Stage A. Without -from it cannot reach the live sources, so it
// reports the requirement plainly rather than pretending to export.
func doExport(ctx context.Context, o options) error {
	if o.from == "" {
		return fmt.Errorf("stage A export against github.com needs network and credentials this build does not carry; pass -from <fixture-snapshot> to re-export a local fixture offline")
	}
	m := realworld.NewManifest(o.tier, "fixture:"+o.from)
	src, err := discoverRepos(o.from)
	if err != nil {
		return err
	}
	ex := realworld.FixtureExporter{Root: o.from}
	if err := realworld.ExportToSnapshot(ctx, ex, src, m, o.data); err != nil {
		return err
	}
	fmt.Printf("exported %d repo(s) to %s\n", len(m.Repos), o.data)
	for _, d := range m.Dropped {
		fmt.Printf("  drop: %s (%d) %s\n", d.What, d.Count, d.Reason)
	}
	return nil
}

// doSeed runs Stage B: read the snapshot, seed each repo, and write the measured
// manifest.
func doSeed(ctx context.Context, o options) error {
	dsn := o.db
	if dsn == "" {
		dsn = os.Getenv("GITHOME_DATABASE_URL")
	}
	if dsn == "" {
		return errors.New("-db or GITHOME_DATABASE_URL is required for the seed stage")
	}
	if o.git != "" {
		fmt.Printf("note: -git %s is reserved for the git mirror stage (network) and is not used by the metadata seed\n", o.git)
	}

	repos, err := discoverRepos(o.data)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("no repos found under %s", o.data)
	}

	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	m := loadOrNewManifest(o)
	m.Pseudonymized = o.pseudonymize
	m.Repos = nil

	for _, ref := range repos {
		res, repo, err := seedOne(ctx, st, o, ref, m.Reactor)
		if err != nil {
			return fmt.Errorf("seed %s: %w", ref.NWO(), err)
		}
		m.Repos = append(m.Repos, realworld.RepoManifest{
			Repo:       repo,
			Provenance: provenanceFor(o.pseudonymize),
			Rows:       res.Rows,
		})
		m.Dropped = append(m.Dropped, res.Dropped...)
		fmt.Printf("seeded %s: %v\n", ref.NWO(), res.Rows)
	}

	v, err := st.Version(ctx)
	if err == nil {
		m.SchemaVersion = int(v)
	}
	if err := m.Save(o.manifest); err != nil {
		return err
	}
	fmt.Printf("wrote manifest %s (tier %s, %d repo(s))\n", o.manifest, m.FixtureTier, len(m.Repos))
	return nil
}

// seedOne seeds a single repo and returns the measured result and the repo ref
// to record in the manifest. The pseudonymized path must hold the whole corpus
// in memory because the login bijection and body redaction span every row, so it
// reads, rewrites, and seeds the materialized corpus. The official path streams
// the snapshot table by table through SeedSnapshot, so a scale repo never loads
// its whole body set into RAM at once.
func seedOne(ctx context.Context, st *store.Store, o options, ref realworld.RepoRef, reactor realworld.ReactorPool) (*realworld.SeedResult, realworld.RepoRef, error) {
	if o.pseudonymize {
		c, err := realworld.ReadCorpus(o.data, ref)
		if err != nil {
			return nil, ref, err
		}
		c = realworld.NewPseudonymizer(true).Apply(c)
		res, err := realworld.SeedCorpus(ctx, st, c, reactor)
		return res, c.Repo, err
	}
	res, err := realworld.SeedSnapshot(ctx, st, o.data, ref, reactor)
	if err != nil {
		return nil, ref, err
	}
	return res, realworld.ReadRepoRef(o.data, ref), nil
}

// loadOrNewManifest reuses an existing manifest's pins if one is present so the
// reactor pool and dataset revision survive a re-seed, else starts a fresh one.
func loadOrNewManifest(o options) *realworld.Manifest {
	if m, err := realworld.LoadManifest(o.manifest); err == nil {
		m.FixtureTier = o.tier
		return m
	}
	return realworld.NewManifest(o.tier, "fixture:"+o.data)
}

func provenanceFor(pseudonymized bool) realworld.Provenance {
	if pseudonymized {
		return realworld.Modeled
	}
	return realworld.Official
}

// discoverRepos lists the repositories present in a snapshot directory by
// reading its manifest when one is present, else by walking the repo/ tree.
func discoverRepos(root string) ([]realworld.RepoRef, error) {
	if m, err := realworld.LoadManifest(filepath.Join(root, realworld.ManifestName)); err == nil && len(m.Repos) > 0 {
		refs := make([]realworld.RepoRef, 0, len(m.Repos))
		for _, r := range m.Repos {
			refs = append(refs, r.Repo)
		}
		return refs, nil
	}
	return walkSnapshotRepos(root)
}

// walkSnapshotRepos finds repo/<owner>/<name> directories under a snapshot root.
func walkSnapshotRepos(root string) ([]realworld.RepoRef, error) {
	base := filepath.Join(root, "repo")
	owners, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var refs []realworld.RepoRef
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(base, owner.Name()))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if name.IsDir() {
				refs = append(refs, realworld.RepoRef{Owner: owner.Name(), Name: name.Name()})
			}
		}
	}
	return refs, nil
}
