package graphql_test

import (
	"encoding/json"
	"testing"
)

// rateLimitQuery reads the rate-limit shape alongside a paged document so the
// cost reflects the whole operation, the way GitHub scores a call.
const rateLimitQuery = `query Cost($owner: String!, $name: String!) {
  rateLimit { limit cost remaining used nodeCount }
  repository(owner: $owner, name: $name) {
    pullRequests(first: 50) {
      nodes { comments(first: 60) { nodes { id } } }
    }
  }
}`

// rateLimitOnlyQuery reads the rate-limit shape with no connections in flight,
// the one-point floor on a call that requests no paged nodes.
const rateLimitOnlyQuery = `{ rateLimit { limit cost remaining used nodeCount } }`

// TestRateLimitReportsNodeCountAndCost confirms rateLimit reports the node count
// and point cost the node-count walk computes: pullRequests(first:50) nesting
// comments(first:60) is 50 + 50*60 = 3050 nodes over 51 requests, which costs
// the one-point minimum (51/100 rounds up to 1).
func TestRateLimitReportsNodeCountAndCost(t *testing.T) {
	fx := pullServer(t)

	got := post(t, fx.srv, fx.token, rateLimitQuery, map[string]any{"owner": "octocat", "name": "hello"})
	rl := rateLimitFields(t, got)
	if rl.NodeCount != 3050 {
		t.Fatalf("nodeCount = %d, want 3050, body %s", rl.NodeCount, got)
	}
	if rl.Cost != 1 {
		t.Fatalf("cost = %d, want 1, body %s", rl.Cost, got)
	}
	if rl.Limit != 5000 {
		t.Fatalf("limit = %d, want 5000, body %s", rl.Limit, got)
	}
	if rl.Remaining != rl.Limit-rl.Cost {
		t.Fatalf("remaining = %d, want %d, body %s", rl.Remaining, rl.Limit-rl.Cost, got)
	}

	got = post(t, fx.srv, fx.token, rateLimitOnlyQuery, nil)
	rl = rateLimitFields(t, got)
	if rl.NodeCount != 0 || rl.Cost != 1 {
		t.Fatalf("bare rateLimit nodeCount=%d cost=%d, want 0/1, body %s", rl.NodeCount, rl.Cost, got)
	}
}

type rateLimitShape struct {
	Limit     int
	Cost      int
	Remaining int
	Used      int
	NodeCount int
}

func rateLimitFields(t *testing.T, body []byte) rateLimitShape {
	t.Helper()
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			RateLimit rateLimitShape `json:"rateLimit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal rateLimit: %v, body %s", err, body)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("rateLimit errors: %v, body %s", env.Errors, body)
	}
	return env.Data.RateLimit
}
