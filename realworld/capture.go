package realworld

import (
	"slices"
	"time"
)

// This file plans the differ-capture sample: the set of real github.com
// responses captured at ingest time that become the golden set the conformance
// differ compares Githome's output against. The plan is what to capture, chosen
// to span each repo's number range so the differ checks old rows and new rows
// alike. Fetching the responses needs network and is done by a Capturer; the
// selection logic is pure and testable offline.

// CaptureKind names one kind of golden response.
type CaptureKind string

// The capture kinds: one golden response per kind, sampled across the number
// range so the differ checks old and new rows of each.
const (
	CaptureIssue    CaptureKind = "issue"
	CapturePull     CaptureKind = "pull"
	CaptureComment  CaptureKind = "comment"
	CaptureTimeline CaptureKind = "timeline"
	CaptureReview   CaptureKind = "review"
	CaptureStatus   CaptureKind = "status"
)

// CaptureItem is one entry in the capture plan: a kind and the subject number to
// fetch. CapturedAt is zero in the plan and stamped when the response is actually
// captured.
type CaptureItem struct {
	Kind       CaptureKind `json:"kind"`
	Number     int64       `json:"number"`
	CapturedAt time.Time   `json:"captured_at,omitzero"`
}

// SelectSampleNumbers returns the earliest, middle, and latest of a set of
// numbers, deduplicated and sorted. A short set returns just its distinct
// members. This is the "span the range" rule: the differ checks the oldest row,
// a middle row, and the newest, so a regression that only touches old or only
// touches new rows is still caught.
func SelectSampleNumbers(numbers []int64) []int64 {
	if len(numbers) == 0 {
		return nil
	}
	sorted := slices.Clone(numbers)
	slices.Sort(sorted)
	picks := []int64{sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1]}
	seen := map[int64]bool{}
	var out []int64
	for _, n := range picks {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// BuildCapturePlan selects the golden-response sample for a corpus: the
// earliest/middle/latest issue, pull request, comment, timeline, review, and
// status, so the differ has old and new rows of every kind. The plan is the
// differ-capture sample manifest the build records.
func BuildCapturePlan(c *Corpus) []CaptureItem {
	var items []CaptureItem

	issueNums := make([]int64, 0, len(c.Issues))
	for _, iss := range c.Issues {
		if !iss.IsPullRequest {
			issueNums = append(issueNums, iss.Number)
		}
	}
	for _, n := range SelectSampleNumbers(issueNums) {
		items = append(items, CaptureItem{Kind: CaptureIssue, Number: n})
		items = append(items, CaptureItem{Kind: CaptureTimeline, Number: n})
	}

	prNums := make([]int64, 0, len(c.PullRequests))
	for _, pr := range c.PullRequests {
		prNums = append(prNums, pr.Number)
	}
	for _, n := range SelectSampleNumbers(prNums) {
		items = append(items, CaptureItem{Kind: CapturePull, Number: n})
	}

	commentIDs := make([]int64, 0, len(c.Comments))
	for _, cm := range c.Comments {
		commentIDs = append(commentIDs, cm.ID)
	}
	for _, n := range SelectSampleNumbers(commentIDs) {
		items = append(items, CaptureItem{Kind: CaptureComment, Number: n})
	}

	reviewIDs := make([]int64, 0, len(c.Reviews))
	for _, r := range c.Reviews {
		reviewIDs = append(reviewIDs, r.ID)
	}
	for _, n := range SelectSampleNumbers(reviewIDs) {
		items = append(items, CaptureItem{Kind: CaptureReview, Number: n})
	}

	// Statuses key on a head sha, not a number; sample the distinct PR numbers
	// whose head the statuses report against by capturing the PR sample again
	// under the status kind, which is what the differ keys the status check on.
	for _, n := range SelectSampleNumbers(prNums) {
		items = append(items, CaptureItem{Kind: CaptureStatus, Number: n})
	}

	return items
}
