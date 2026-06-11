package rest

import (
	"encoding/json"
	"net/http"
	"testing"
)

// decodePullList decodes a pulls list body into the loose map shape the
// filter assertions read numbers out of.
func decodePullList(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode pull list: %v, body %s", err, body)
	}
	return out
}

// pullNumbers projects the number field off each listed pull request.
func pullNumbers(t *testing.T, body []byte) []int64 {
	t.Helper()
	list := decodePullList(t, body)
	out := make([]int64, 0, len(list))
	for _, item := range list {
		n, ok := item["number"].(float64)
		if !ok {
			t.Fatalf("pull without a number: %v", item)
		}
		out = append(out, int64(n))
	}
	return out
}

// openSecondPull branches feature2 off the feature tip via the refs API and
// opens feature2 -> main as pull request #2.
func (fx pullFixture) openSecondPull(t *testing.T) {
	t.Helper()
	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/git/ref/heads/feature", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read feature ref status %d, body %s", resp.StatusCode, body)
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(body, &ref); err != nil {
		t.Fatalf("decode ref: %v", err)
	}
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"refs/heads/feature2","sha":"`+ref.Object.SHA+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create feature2 status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls", fx.token,
		`{"title":"Second","head":"feature2","base":"main"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open second pull status %d, body %s", resp.StatusCode, body)
	}
}

// TestPullsListHeadBaseFilters covers the head and base query params on the
// pulls list: the "owner:branch" head form, the bare branch form, and the
// base branch filter. Renovate leans on head for its dedupe check.
func TestPullsListHeadBaseFilters(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	fx.openSecondPull(t)

	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls?head=octocat:feature")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("head filter status %d, body %s", resp.StatusCode, body)
	}
	if nums := pullNumbers(t, body); len(nums) != 1 || nums[0] != 1 {
		t.Errorf("head=octocat:feature numbers %v, want [1]", nums)
	}

	_, body = get(t, fx.srv, "/repos/octocat/hello/pulls?head=feature2")
	if nums := pullNumbers(t, body); len(nums) != 1 || nums[0] != 2 {
		t.Errorf("head=feature2 numbers %v, want [2]", nums)
	}

	_, body = get(t, fx.srv, "/repos/octocat/hello/pulls?head=stranger:feature")
	if nums := pullNumbers(t, body); len(nums) != 0 {
		t.Errorf("head=stranger:feature numbers %v, want none", nums)
	}

	_, body = get(t, fx.srv, "/repos/octocat/hello/pulls?base=main")
	if nums := pullNumbers(t, body); len(nums) != 2 {
		t.Errorf("base=main numbers %v, want two", nums)
	}

	_, body = get(t, fx.srv, "/repos/octocat/hello/pulls?base=dev")
	if nums := pullNumbers(t, body); len(nums) != 0 {
		t.Errorf("base=dev numbers %v, want none", nums)
	}
}

// TestPullsListSortDirection covers sort and direction: the default is newest
// first, direction=asc flips it, and sort=updated orders on the update stamp.
func TestPullsListSortDirection(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	fx.openSecondPull(t)

	_, body := get(t, fx.srv, "/repos/octocat/hello/pulls")
	if nums := pullNumbers(t, body); len(nums) != 2 || nums[0] != 2 {
		t.Errorf("default order numbers %v, want [2 1]", nums)
	}

	_, body = get(t, fx.srv, "/repos/octocat/hello/pulls?direction=asc")
	if nums := pullNumbers(t, body); len(nums) != 2 || nums[0] != 1 {
		t.Errorf("direction=asc numbers %v, want [1 2]", nums)
	}

	// Touching pull #1 makes it the most recently updated.
	if resp, b := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/pulls/1", fx.token,
		`{"title":"Add a feature, retitled"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("retitle status %d, body %s", resp.StatusCode, b)
	}
	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls?sort=updated&direction=desc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sort=updated status %d, body %s", resp.StatusCode, body)
	}
	if nums := pullNumbers(t, body); len(nums) != 2 || nums[0] != 1 {
		t.Errorf("sort=updated numbers %v, want [1 2]", nums)
	}
}
