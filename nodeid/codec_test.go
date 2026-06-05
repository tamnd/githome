package nodeid

import "testing"

var allKinds = []Kind{
	KindUser, KindOrganization, KindRepository, KindIssue, KindPullRequest,
	KindIssueComment, KindPullRequestReview, KindPullRequestReviewComment,
	KindPullRequestReviewThread, KindCheckRun, KindCheckSuite, KindStatusContext,
	KindLabel, KindMilestone, KindCommit,
}

func TestRoundTripBothFormats(t *testing.T) {
	ids := []int64{0, 1, 2, 41, 100, 1234567, 9_007_199_254_740_991}
	for _, format := range []Format{FormatNew, FormatLegacy} {
		for _, kind := range allKinds {
			for _, id := range ids {
				enc := Encode(kind, id, format)
				if enc == "" {
					t.Fatalf("Encode(%v, %d, %v) returned empty", kind, id, format)
				}
				gotKind, gotID, err := Decode(enc)
				if err != nil {
					t.Fatalf("Decode(%q): %v", enc, err)
				}
				if gotKind != kind || gotID != id {
					t.Fatalf("round trip (format %v): want (%v,%d) got (%v,%d) via %q",
						format, kind, id, gotKind, gotID, enc)
				}
			}
		}
	}
}

func TestNewFormatIsPrefixed(t *testing.T) {
	enc := Encode(KindPullRequest, 42, FormatNew)
	if got := enc[:3]; got != "PR_" {
		t.Fatalf("new pull-request node id should start with PR_, got %q", enc)
	}
}

func TestLegacyDecodesKnownVector(t *testing.T) {
	// base64("04:User1") is the historical encoding for user databaseId 1.
	const legacy = "MDQ6VXNlcjE="
	kind, id, err := Decode(legacy)
	if err != nil {
		t.Fatalf("Decode(%q): %v", legacy, err)
	}
	if kind != KindUser || id != 1 {
		t.Fatalf("want (User,1) got (%v,%d)", kind, id)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not-base64-!@#", "U_!!!", "MDppbnZhbGlk"} {
		if _, _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%q) should have failed", bad)
		}
	}
}

func TestEncodeUnknownKind(t *testing.T) {
	if got := Encode(Kind(999), 1, FormatNew); got != "" {
		t.Fatalf("unknown kind should encode to empty, got %q", got)
	}
}
