package realworld

import (
	"slices"
	"testing"
)

func TestSelectSampleNumbersSpansRange(t *testing.T) {
	got := SelectSampleNumbers([]int64{5, 1, 9, 3, 7})
	want := []int64{1, 5, 9} // earliest, middle, latest
	if !slices.Equal(got, want) {
		t.Errorf("sample not earliest/middle/latest: got %v want %v", got, want)
	}
}

func TestSelectSampleNumbersDedupesShortSets(t *testing.T) {
	if got := SelectSampleNumbers([]int64{42}); !slices.Equal(got, []int64{42}) {
		t.Errorf("single element: got %v", got)
	}
	if got := SelectSampleNumbers([]int64{2, 8}); !slices.Equal(got, []int64{2, 8}) {
		t.Errorf("two elements should not duplicate the middle: got %v", got)
	}
	if got := SelectSampleNumbers(nil); got != nil {
		t.Errorf("empty input should yield nil: %v", got)
	}
}

func TestBuildCapturePlanCoversEveryKind(t *testing.T) {
	plan := BuildCapturePlan(sampleCorpus())
	kinds := map[CaptureKind]int{}
	for _, it := range plan {
		kinds[it.Kind]++
		if !it.CapturedAt.IsZero() {
			t.Errorf("plan items must not be stamped captured yet: %+v", it)
		}
	}
	for _, want := range []CaptureKind{CaptureIssue, CapturePull, CaptureComment, CaptureReview, CaptureTimeline, CaptureStatus} {
		if kinds[want] == 0 {
			t.Errorf("capture plan missing kind %q: %+v", want, kinds)
		}
	}
	// The sample corpus has one real issue (number 100); the PR (101) is the
	// pull half and must not be captured as an issue.
	for _, it := range plan {
		if it.Kind == CaptureIssue && it.Number == 101 {
			t.Errorf("pull request captured as an issue")
		}
	}
}
