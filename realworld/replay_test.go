package realworld

import (
	"testing"
)

func TestMixForKnownAndUnknownRepos(t *testing.T) {
	if MixFor("microsoft/vscode")[OpRMeta] != 50 {
		t.Errorf("vscode mix wrong: %+v", MixFor("microsoft/vscode"))
	}
	// An unrecognized repo falls back to the aggregate platform mix.
	if MixFor("acme/widgets")[OpXCond] != 45 {
		t.Errorf("unknown repo did not fall back to the platform mix: %+v", MixFor("acme/widgets"))
	}
	// Every known mix sums to 100 so it reads as whole-number percentages.
	for nwo, mix := range repoMixes {
		total := 0
		for _, w := range mix {
			total += w
		}
		if total != 100 {
			t.Errorf("%s mix does not sum to 100: %d", nwo, total)
		}
	}
}

func TestSyntheticMixMatchesProportionsExactly(t *testing.T) {
	mix := RequestMix{OpXCond: 45, OpRMeta: 30, OpRGit: 12, OpTGit: 8, OpWMeta: 5}
	plan := PlanSyntheticMix("acme/widgets", mix, 1000, 100)
	if len(plan.Requests) != 1000 {
		t.Fatalf("wrong request count: %d", len(plan.Requests))
	}
	got := map[OpClass]int{}
	for _, r := range plan.Requests {
		got[r.Class]++
	}
	// Largest-remainder over 1000 with these whole percentages is exact.
	want := map[OpClass]int{OpXCond: 450, OpRMeta: 300, OpRGit: 120, OpTGit: 80, OpWMeta: 50}
	for c, w := range want {
		if got[c] != w {
			t.Errorf("class %s: got %d want %d", c, got[c], w)
		}
	}
	// Arrivals are evenly spaced and monotonic.
	if plan.Requests[1].Offset <= plan.Requests[0].Offset {
		t.Errorf("offsets not increasing: %v then %v", plan.Requests[0].Offset, plan.Requests[1].Offset)
	}
}

func TestSyntheticMixIsDeterministic(t *testing.T) {
	mix := MixFor("kubernetes/kubernetes")
	a := PlanSyntheticMix("kubernetes/kubernetes", mix, 137, 50)
	b := PlanSyntheticMix("kubernetes/kubernetes", mix, 137, 50)
	if len(a.Requests) != len(b.Requests) {
		t.Fatalf("length differs: %d vs %d", len(a.Requests), len(b.Requests))
	}
	for i := range a.Requests {
		if a.Requests[i] != b.Requests[i] {
			t.Fatalf("request %d differs: %+v vs %+v", i, a.Requests[i], b.Requests[i])
		}
	}
}

func TestTraceDrivenPreservesBurstinessAndRecordsCompression(t *testing.T) {
	c := sampleCorpus()
	const compression = 60
	plan := PlanTraceDriven(c, compression, 2)

	if plan.Mode != TraceDriven || plan.Compression != compression {
		t.Fatalf("mode or compression not recorded: %+v", plan)
	}
	// The first write starts at offset zero (time is relative to the earliest
	// event), and offsets are sorted.
	if plan.Requests[0].Offset != 0 {
		t.Errorf("trace does not start at zero: %v", plan.Requests[0].Offset)
	}
	for i := 1; i < len(plan.Requests); i++ {
		if plan.Requests[i].Offset < plan.Requests[i-1].Offset {
			t.Fatalf("requests not sorted by offset at %d", i)
		}
	}
	// Writes ride the real timeline; reads are injected between them, so a
	// readWriteRatio of 2 produces more requests than there are writes.
	writes := 0
	for _, r := range plan.Requests {
		if r.Class == OpWMeta {
			writes++
		}
	}
	if writes == 0 {
		t.Fatal("no writes extracted from the corpus")
	}
	if len(plan.Requests) <= writes {
		t.Errorf("no reads were interleaved: %d requests for %d writes", len(plan.Requests), writes)
	}
}

func TestTraceDrivenEmptyCorpus(t *testing.T) {
	plan := PlanTraceDriven(&Corpus{Repo: RepoRef{Owner: "a", Name: "b"}}, 10, 1)
	if len(plan.Requests) != 0 {
		t.Errorf("empty corpus should yield no requests: %d", len(plan.Requests))
	}
}
