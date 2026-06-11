package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"
)

// rateHeaders pulls the X-RateLimit-* headers into ints for assertion.
func rateHeaders(t *testing.T, resp *http.Response) (limit, remaining, used int, resource string) {
	t.Helper()
	atoi := func(name string) int {
		v := resp.Header.Get(name)
		if v == "" {
			t.Fatalf("missing %s header", name)
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("%s = %q, not an integer", name, v)
		}
		return n
	}
	if resp.Header.Get("X-RateLimit-Reset") == "" {
		t.Fatal("missing X-RateLimit-Reset header")
	}
	return atoi("X-RateLimit-Limit"), atoi("X-RateLimit-Remaining"), atoi("X-RateLimit-Used"),
		resp.Header.Get("X-RateLimit-Resource")
}

// TestRateLimitMetering checks the headers count real spend: each anonymous
// request charges the shared IP bucket, and GET /rate_limit reports the same
// numbers without consuming any itself.
func TestRateLimitMetering(t *testing.T) {
	srv := testServer(t)

	resp, _ := get(t, srv, "/meta")
	limit, remaining, used, resource := rateHeaders(t, resp)
	if limit != 60 || used != 1 || remaining != 59 || resource != "core" {
		t.Errorf("first request limit/remaining/used/resource = %d/%d/%d/%s, want 60/59/1/core",
			limit, remaining, used, resource)
	}

	resp, _ = get(t, srv, "/meta")
	if _, _, used, _ := rateHeaders(t, resp); used != 2 {
		t.Errorf("second request used = %d, want 2", used)
	}

	// /rate_limit reports the spend so far and does not add to it.
	for i := 0; i < 2; i++ {
		resp, body := get(t, srv, "/rate_limit")
		limit, remaining, used, _ = rateHeaders(t, resp)
		if limit != 60 || used != 2 || remaining != 58 {
			t.Errorf("rate_limit headers limit/remaining/used = %d/%d/%d, want 60/58/2", limit, remaining, used)
		}
		var rl struct {
			Resources struct {
				Core struct {
					Limit     int `json:"limit"`
					Remaining int `json:"remaining"`
					Used      int `json:"used"`
				} `json:"core"`
			} `json:"resources"`
			Rate struct {
				Used int `json:"used"`
			} `json:"rate"`
		}
		if err := json.Unmarshal(body, &rl); err != nil {
			t.Fatalf("decode rate_limit body: %v", err)
		}
		if rl.Resources.Core.Limit != 60 || rl.Resources.Core.Used != 2 || rl.Resources.Core.Remaining != 58 {
			t.Errorf("rate_limit body core = %+v, want limit 60 used 2 remaining 58", rl.Resources.Core)
		}
		if rl.Rate.Used != 2 {
			t.Errorf("rate_limit body rate.used = %d, want 2", rl.Rate.Used)
		}
	}
}

// TestRateLimitExhaustion runs a tiny anonymous budget dry and checks the 403:
// GitHub's primary-limit message, the documentation_url, a Retry-After, and a
// remaining of 0 that stays at 0.
func TestRateLimitExhaustion(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimit.AnonPerHour = 2
	root := mizu.NewRouter()
	Mount(root, Deps{Config: cfg})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	for i := 0; i < 2; i++ {
		if resp, _ := get(t, srv, "/meta"); resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d status %d, want 200", i+1, resp.StatusCode)
		}
	}
	resp, body := get(t, srv, "/meta")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("exhausted status %d, want 403, body %s", resp.StatusCode, body)
	}
	if _, remaining, used, _ := rateHeaders(t, resp); remaining != 0 || used != 2 {
		t.Errorf("exhausted remaining/used = %d/%d, want 0/2", remaining, used)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("exhausted 403 missing Retry-After")
	}
	if !strings.Contains(string(body), "API rate limit exceeded for") {
		t.Errorf("403 body missing rate limit message: %s", body)
	}
	if !strings.Contains(string(body), "rate-limits-for-the-rest-api") {
		t.Errorf("403 body missing documentation_url: %s", body)
	}

	// /rate_limit still answers after exhaustion, the escape hatch GitHub keeps open.
	if resp, _ := get(t, srv, "/rate_limit"); resp.StatusCode != http.StatusOK {
		t.Errorf("rate_limit after exhaustion status %d, want 200", resp.StatusCode)
	}
}

// TestRateLimitAuthedBucket checks an authenticated request spends the user's
// own 5000-an-hour budget, not the anonymous IP bucket, and that a bad
// credential's 401 still carries the headers, charged against the IP.
func TestRateLimitAuthedBucket(t *testing.T) {
	srv, token := authServer(t)

	// Anonymous spend lands on the IP bucket.
	resp, _ := get(t, srv, "/meta")
	if limit, _, used, _ := rateHeaders(t, resp); limit != 60 || used != 1 {
		t.Errorf("anon limit/used = %d/%d, want 60/1", limit, used)
	}

	// The authenticated request opens its own fresh bucket.
	resp, body := authedGet(t, srv, "/user", "token "+token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authed GET /user status %d, body %s", resp.StatusCode, body)
	}
	limit, remaining, used, resource := rateHeaders(t, resp)
	if limit != 5000 || remaining != 4999 || used != 1 || resource != "core" {
		t.Errorf("authed limit/remaining/used/resource = %d/%d/%d/%s, want 5000/4999/1/core",
			limit, remaining, used, resource)
	}

	// A bad credential is a 401 that still carries the headers, on the IP bucket.
	resp, _ = authedGet(t, srv, "/user", "token ghp_definitelynotvalid")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad credential status %d, want 401", resp.StatusCode)
	}
	if limit, _, used, _ := rateHeaders(t, resp); limit != 60 || used != 2 {
		t.Errorf("bad-credential limit/used = %d/%d, want 60/2", limit, used)
	}
}

// TestRateLimitSearchResource checks search requests spend the separate
// per-minute search bucket: resource name, the anonymous 10-a-minute budget,
// and no charge to core.
func TestRateLimitSearchResource(t *testing.T) {
	fx := searchServer(t)

	resp, _ := get(t, fx.srv, "/search/issues?q=x")
	limit, remaining, used, resource := rateHeaders(t, resp)
	if resource != "search" || limit != 10 || remaining != 9 || used != 1 {
		t.Errorf("search limit/remaining/used/resource = %d/%d/%d/%s, want 10/9/1/search",
			limit, remaining, used, resource)
	}

	// Core was not charged by the search call.
	resp, _ = get(t, fx.srv, "/meta")
	if limit, _, used, _ := rateHeaders(t, resp); limit != 60 || used != 1 {
		t.Errorf("core after search limit/used = %d/%d, want 60/1", limit, used)
	}
}
