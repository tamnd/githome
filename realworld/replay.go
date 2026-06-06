package realworld

import (
	"sort"
	"strings"
	"time"
)

// This file builds the load schedules of the workload-replay model. It produces
// the request schedule as data; it does not fire the requests. Firing the
// schedule against a running server is the job of the open-model HTTP load
// harness (the benchload tool of the performance series), which consumes a
// schedule like the one ScheduledRequest carries and replays it independent of
// how fast the server answers, so a slow server builds a queue rather than
// throttling the offered load. Keeping the schedule a pure, deterministic
// artifact is what lets the mixes and the trace shape be unit-tested offline.

// OpClass is one of the five operation classes the SLOs are stated against. The
// per-repo mix weights how often each class appears in a synthetic replay.
type OpClass string

const (
	// OpXCond is a conditional read: a GET that the client expects to answer
	// 304 from an ETag or since-cursor (the poll flood).
	OpXCond OpClass = "X-cond"
	// OpRMeta is a metadata read: an issue view, a list page, a PR view.
	OpRMeta OpClass = "R-meta"
	// OpRGit is a git read served over HTTP: a tree or blob fetch.
	OpRGit OpClass = "R-git"
	// OpTGit is a git transport operation: a clone or fetch.
	OpTGit OpClass = "T-git"
	// OpWMeta is a metadata write: open an issue, comment, merge a PR, apply a
	// label.
	OpWMeta OpClass = "W-meta"
)

// opClassOrder is the canonical class order, so a schedule's class assignment
// does not depend on map iteration order.
var opClassOrder = []OpClass{OpXCond, OpRMeta, OpRGit, OpTGit, OpWMeta}

// RequestMix is the per-class weighting of a repo's load, in whole-number
// percentages that sum to 100. It is a MODELED shape: it is derived from the
// repo's real read/write character and the subsystem that repo stresses, not
// measured per request, so it is recorded in the manifest as such.
type RequestMix map[OpClass]int

// repoMixes is the per-repo request mix. The aggregate platform mix (45 X-cond,
// 30 R-meta, 12 R-git, 8 T-git, 5 W-meta) is an average; replaying each repo at
// its own shape is the point, because a repo-blind uniform mix would hide the
// very stress each repo was chosen for. linux is transport-dominated, nixpkgs
// and kubernetes are write-dominated (kubernetes event-amplified), vscode is
// read-and-poll-dominated, rust is review-read-dominated.
var repoMixes = map[string]RequestMix{
	"torvalds/linux":        {OpXCond: 10, OpRMeta: 0, OpRGit: 25, OpTGit: 65, OpWMeta: 0},
	"NixOS/nixpkgs":         {OpXCond: 15, OpRMeta: 20, OpRGit: 15, OpTGit: 15, OpWMeta: 35},
	"kubernetes/kubernetes": {OpXCond: 20, OpRMeta: 20, OpRGit: 0, OpTGit: 5, OpWMeta: 55},
	"microsoft/vscode":      {OpXCond: 45, OpRMeta: 50, OpRGit: 0, OpTGit: 0, OpWMeta: 5},
	"rust-lang/rust":        {OpXCond: 15, OpRMeta: 35, OpRGit: 20, OpTGit: 15, OpWMeta: 15},
}

// defaultMix is used for a repo with no entry in repoMixes: the aggregate
// platform mix, so an unrecognized repo still replays a sane shape.
var defaultMix = RequestMix{OpXCond: 45, OpRMeta: 30, OpRGit: 12, OpTGit: 8, OpWMeta: 5}

// MixFor returns the request mix for a repo by owner/name, falling back to the
// aggregate platform mix.
func MixFor(nwo string) RequestMix {
	if m, ok := repoMixes[nwo]; ok {
		return m
	}
	return defaultMix
}

// ReplayMode names how a schedule's arrivals were generated.
type ReplayMode string

const (
	// SyntheticMix drives the request mix at a fixed rate; it answers whether the
	// SLOs hold at a chosen size and rate.
	SyntheticMix ReplayMode = "synthetic-mix"
	// TraceDriven replays the real event timeline, time-compressed, so the load
	// carries the real burstiness rather than a smooth arrival.
	TraceDriven ReplayMode = "trace-driven"
)

// ScheduledRequest is one planned request: its offset from the start of the
// replay, its operation class, the repo it targets, and the subject it touches
// (an issue or PR number for a metadata op, empty for a transport op). It is the
// unit the load harness fires.
type ScheduledRequest struct {
	Offset time.Duration `json:"offset"`
	Class  OpClass       `json:"class"`
	Repo   string        `json:"repo"`
	Number int64         `json:"number,omitempty"`
}

// ReplayPlan is the full schedule for one repo, plus the parameters that shaped
// it, so a run is reproducible and a reviewer can see why the load looks the way
// it does. Compression and ReadWriteRatio are recorded for the trace-driven mode
// per the no-silent-caps rule.
type ReplayPlan struct {
	Repo           string             `json:"repo"`
	Mode           ReplayMode         `json:"mode"`
	Mix            RequestMix         `json:"mix,omitempty"`
	Compression    float64            `json:"compression,omitempty"`
	ReadWriteRatio int                `json:"read_write_ratio,omitempty"`
	Requests       []ScheduledRequest `json:"requests"`
}

// PlanSyntheticMix builds a fixed-rate schedule that holds the repo's request
// mix exactly over count requests at rps requests per second. Arrivals are
// evenly spaced (the harness's open model adds no jitter of its own), and the
// class of each arrival is assigned by the largest-remainder method so the realized
// class counts match the mix proportions exactly and deterministically — no RNG,
// so two builds of the same plan are identical.
func PlanSyntheticMix(repo string, mix RequestMix, count, rps int) ReplayPlan {
	plan := ReplayPlan{Repo: repo, Mode: SyntheticMix, Mix: mix}
	if count <= 0 || rps <= 0 {
		return plan
	}
	classes := allocateClasses(mix, count)
	dt := time.Second / time.Duration(rps)
	plan.Requests = make([]ScheduledRequest, count)
	for i := range count {
		plan.Requests[i] = ScheduledRequest{Offset: time.Duration(i) * dt, Class: classes[i], Repo: repo}
	}
	return plan
}

// allocateClasses turns a percentage mix into an exact per-request class
// sequence of length count using the largest-remainder method, then interleaves
// the classes so a run is not all of one class followed by all of the next.
func allocateClasses(mix RequestMix, count int) []OpClass {
	// Ideal (possibly fractional) count per class.
	type quota struct {
		class OpClass
		whole int
		frac  float64
	}
	total := 0
	for _, c := range opClassOrder {
		total += mix[c]
	}
	if total == 0 {
		total = 1
	}
	quotas := make([]quota, 0, len(opClassOrder))
	assigned := 0
	for _, c := range opClassOrder {
		ideal := float64(mix[c]) / float64(total) * float64(count)
		w := int(ideal)
		quotas = append(quotas, quota{class: c, whole: w, frac: ideal - float64(w)})
		assigned += w
	}
	// Hand out the remaining slots to the largest fractional remainders.
	rem := count - assigned
	order := make([]int, len(quotas))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return quotas[order[a]].frac > quotas[order[b]].frac })
	for i := range rem {
		quotas[order[i]].whole++
	}
	// Interleave: emit classes round-robin weighted by their remaining count so
	// the sequence is well mixed rather than blocked by class.
	out := make([]OpClass, 0, count)
	left := make([]int, len(quotas))
	for i := range quotas {
		left[i] = quotas[i].whole
	}
	for len(out) < count {
		progressed := false
		for i := range quotas {
			if left[i] > 0 {
				out = append(out, quotas[i].class)
				left[i]--
				progressed = true
				if len(out) == count {
					break
				}
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// writeArrival is one real state-changing event extracted from a corpus, with
// its real timestamp and the subject it touched.
type writeArrival struct {
	at     time.Time
	number int64
}

// PlanTraceDriven builds a schedule from the corpus's real event timeline. It
// extracts every state-changing event with its real timestamp (issue opened,
// comment added, PR merged, every timeline event), sorts by time, and
// time-compresses by compression so a year of history replays in a tractable
// wall-clock while the relative burstiness is preserved — a release-day spike
// stays a spike. Between writes it injects readWriteRatio reads of the repo's
// read classes, so the replay is not write-only. The compression factor and the
// ratio are carried on the plan so the run records them.
func PlanTraceDriven(c *Corpus, compression float64, readWriteRatio int) ReplayPlan {
	repo := c.Repo.NWO()
	plan := ReplayPlan{Repo: repo, Mode: TraceDriven, Mix: MixFor(repo), Compression: compression, ReadWriteRatio: readWriteRatio}
	if compression <= 0 {
		compression = 1
	}
	plan.Compression = compression

	writes := collectWrites(c)
	if len(writes) == 0 {
		return plan
	}
	sort.SliceStable(writes, func(a, b int) bool { return writes[a].at.Before(writes[b].at) })
	t0 := writes[0].at

	readClasses := readClassesFor(plan.Mix)
	for i, w := range writes {
		off := time.Duration(float64(w.at.Sub(t0)) / compression)
		plan.Requests = append(plan.Requests, ScheduledRequest{Offset: off, Class: OpWMeta, Repo: repo, Number: w.number})
		// Fill the gap to the next write with interleaved reads, so the read load
		// rides the same burstiness as the writes.
		if readWriteRatio <= 0 {
			continue
		}
		next := plan.totalSpan(writes, i, t0, compression)
		gap := next - off
		for r := 0; r < readWriteRatio && gap > 0; r++ {
			frac := time.Duration(int64(gap) * int64(r+1) / int64(readWriteRatio+1))
			plan.Requests = append(plan.Requests, ScheduledRequest{
				Offset: off + frac,
				Class:  readClasses[(i+r)%len(readClasses)],
				Repo:   repo,
				Number: w.number,
			})
		}
	}
	sort.SliceStable(plan.Requests, func(a, b int) bool { return plan.Requests[a].Offset < plan.Requests[b].Offset })
	return plan
}

// totalSpan returns the compressed offset of the write after index i, or the
// offset of the last write plus one second's worth of room when i is the last.
func (ReplayPlan) totalSpan(writes []writeArrival, i int, t0 time.Time, compression float64) time.Duration {
	if i+1 < len(writes) {
		return time.Duration(float64(writes[i+1].at.Sub(t0)) / compression)
	}
	last := time.Duration(float64(writes[len(writes)-1].at.Sub(t0)) / compression)
	return last + time.Second
}

// collectWrites extracts every state-changing event from a corpus as a write
// arrival keyed on its real timestamp.
func collectWrites(c *Corpus) []writeArrival {
	var w []writeArrival
	for _, iss := range c.Issues {
		w = append(w, writeArrival{at: iss.CreatedAt, number: iss.Number})
	}
	for _, cm := range c.Comments {
		w = append(w, writeArrival{at: cm.CreatedAt, number: cm.IssueNumber})
	}
	for _, pr := range c.PullRequests {
		if pr.MergedAt != nil {
			w = append(w, writeArrival{at: *pr.MergedAt, number: pr.Number})
		}
	}
	for _, ev := range c.TimelineEvents {
		w = append(w, writeArrival{at: ev.CreatedAt, number: ev.IssueNumber})
	}
	return w
}

// readClassesFor returns the read-side classes present in a mix, in canonical
// order, so the interleaved reads draw from the classes the repo actually serves.
// A mix with no read weight still reads metadata, since a replay that injected no
// reads at all would not exercise the read path.
func readClassesFor(mix RequestMix) []OpClass {
	var out []OpClass
	for _, c := range opClassOrder {
		if strings.HasPrefix(string(c), "R-") || strings.HasPrefix(string(c), "X-") {
			if mix[c] > 0 {
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		out = []OpClass{OpRMeta}
	}
	return out
}
