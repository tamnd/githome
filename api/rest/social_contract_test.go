package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/store"
)

// social_contract_test.go covers the star, watch, and follow families end to
// end through the mounted router: the idempotent PUT/DELETE writes, the 204/404
// existence checks, and the paginated listings. See 2001/review/01 R01-27.

// userToken mints a fresh PAT carrying the "user" scope for an existing
// account. The follow endpoints gate writes on the user scope, which the
// fixture's repo-scoped token does not carry, so the follow tests act through
// this token instead.
func (fx repoFixture) userToken(t *testing.T, userPK int64) string {
	t.Helper()
	ctx := context.Background()
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := fx.st.InsertToken(ctx, &store.TokenRow{
		UserPK: &userPK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "user",
	}); err != nil {
		t.Fatalf("insert user-scoped token: %v", err)
	}
	return g.Plaintext
}

func decodeArray(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode array: %v, body %s", err, body)
	}
	return list
}

// TestStarringFlow covers PUT/DELETE /user/starred, the check, the stargazers
// list, and the user's starred list.
func TestStarringFlow(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")

	// Not starred yet: the check is 404.
	resp, body := authedGet(t, fx.srv, "/user/starred/octocat/hello", "token "+hubber)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pre-star check %d, want 404, body %s", resp.StatusCode, body)
	}

	// Star it: 204, and a repeat is still 204.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/user/starred/octocat/hello", hubber, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("star %d, want 204, body %s", resp.StatusCode, body)
	}
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/user/starred/octocat/hello", hubber, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("repeat star %d, want 204, body %s", resp.StatusCode, body)
	}

	// Check now 204.
	resp, _ = authedGet(t, fx.srv, "/user/starred/octocat/hello", "token "+hubber)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("post-star check %d, want 204", resp.StatusCode)
	}

	// Stargazers list shows hubber.
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/stargazers", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stargazers %d, body %s", resp.StatusCode, body)
	}
	gazers := decodeArray(t, body)
	if len(gazers) != 1 || gazers[0]["login"] != "hubber" {
		t.Fatalf("stargazers = %v, want [hubber]", gazers)
	}

	// hubber's starred list shows octocat/hello.
	resp, body = authedGet(t, fx.srv, "/users/hubber/starred", "token "+hubber)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("starred list %d, body %s", resp.StatusCode, body)
	}
	starred := decodeArray(t, body)
	if len(starred) != 1 || starred[0]["full_name"] != "octocat/hello" {
		t.Fatalf("starred = %v, want [octocat/hello]", starred)
	}

	// Unstar: 204, idempotent, and the check returns to 404.
	resp, body = authedSend(t, fx.srv, http.MethodDelete, "/user/starred/octocat/hello", hubber, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unstar %d, want 204, body %s", resp.StatusCode, body)
	}
	resp, _ = authedSend(t, fx.srv, http.MethodDelete, "/user/starred/octocat/hello", hubber, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("repeat unstar %d, want 204", resp.StatusCode)
	}
	resp, _ = authedGet(t, fx.srv, "/user/starred/octocat/hello", "token "+hubber)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-unstar check %d, want 404", resp.StatusCode)
	}
}

// TestStarMissingRepo confirms a star on a repository that does not exist 404s.
func TestStarMissingRepo(t *testing.T) {
	fx := repoServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/user/starred/octocat/nope", fx.token, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("star missing repo %d, want 404, body %s", resp.StatusCode, body)
	}
}

// TestStarRequiresAuth confirms the actor-scoped star endpoints reject an
// anonymous caller.
func TestStarRequiresAuth(t *testing.T) {
	fx := repoServer(t)
	resp, _ := authedGet(t, fx.srv, "/user/starred", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon /user/starred %d, want 401", resp.StatusCode)
	}
}

// TestWatchingFlow covers PUT/GET/DELETE /repos/{o}/{r}/subscription, the
// subscribers list, and the user's subscriptions list.
func TestWatchingFlow(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")

	// No subscription yet: GET is 404.
	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/subscription", "token "+hubber)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pre-watch get %d, want 404, body %s", resp.StatusCode, body)
	}

	// Subscribe.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/subscription", hubber,
		`{"subscribed":true,"ignored":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch put %d, want 200, body %s", resp.StatusCode, body)
	}
	sub := decodeObject(t, body)
	if sub["subscribed"] != true || sub["ignored"] != false {
		t.Fatalf("subscription = %v, want subscribed/not-ignored", sub)
	}
	if sub["repository_url"] == nil || sub["url"] == nil {
		t.Fatalf("subscription missing urls: %v", sub)
	}

	// GET now returns the subscription.
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/subscription", "token "+hubber)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch get %d, want 200, body %s", resp.StatusCode, body)
	}

	// Subscribers list shows hubber.
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/subscribers", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribers %d, body %s", resp.StatusCode, body)
	}
	subs := decodeArray(t, body)
	if len(subs) != 1 || subs[0]["login"] != "hubber" {
		t.Fatalf("subscribers = %v, want [hubber]", subs)
	}

	// hubber's subscriptions show octocat/hello.
	resp, body = authedGet(t, fx.srv, "/users/hubber/subscriptions", "token "+hubber)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscriptions %d, body %s", resp.StatusCode, body)
	}
	watched := decodeArray(t, body)
	if len(watched) != 1 || watched[0]["full_name"] != "octocat/hello" {
		t.Fatalf("subscriptions = %v, want [octocat/hello]", watched)
	}

	// Ignoring still records a subscription row but drops from the watchers list.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/subscription", hubber,
		`{"ignored":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ignore put %d, body %s", resp.StatusCode, body)
	}
	sub = decodeObject(t, body)
	if sub["ignored"] != true {
		t.Fatalf("ignored subscription = %v", sub)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/subscribers", "token "+fx.token)
	subs = decodeArray(t, body)
	if len(subs) != 0 {
		t.Fatalf("subscribers after ignore = %v, want []", subs)
	}

	// Unsubscribe: 204, and GET returns to 404.
	resp, _ = authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/subscription", hubber, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unwatch %d, want 204", resp.StatusCode)
	}
	resp, _ = authedGet(t, fx.srv, "/repos/octocat/hello/subscription", "token "+hubber)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-unwatch get %d, want 404", resp.StatusCode)
	}
}

// TestFollowingFlow covers PUT/DELETE /user/following, both check endpoints,
// and the four list endpoints.
func TestFollowingFlow(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")
	octo := fx.userToken(t, fx.ownerPK)

	// octocat does not follow hubber yet.
	resp, _ := authedGet(t, fx.srv, "/user/following/hubber", "token "+octo)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pre-follow check %d, want 404", resp.StatusCode)
	}

	// Follow: 204, idempotent.
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/user/following/hubber", octo, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("follow %d, want 204, body %s", resp.StatusCode, body)
	}
	resp, _ = authedSend(t, fx.srv, http.MethodPut, "/user/following/hubber", octo, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("repeat follow %d, want 204", resp.StatusCode)
	}

	// Both check forms now 204.
	resp, _ = authedGet(t, fx.srv, "/user/following/hubber", "token "+octo)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("actor follow check %d, want 204", resp.StatusCode)
	}
	resp, _ = authedGet(t, fx.srv, "/users/octocat/following/hubber", "token "+octo)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("public follow check %d, want 204", resp.StatusCode)
	}

	// octocat's following list shows hubber; hubber's followers show octocat.
	resp, body = authedGet(t, fx.srv, "/user/following", "token "+octo)
	following := decodeArray(t, body)
	if len(following) != 1 || following[0]["login"] != "hubber" {
		t.Fatalf("following = %v, want [hubber]", following)
	}
	resp, body = authedGet(t, fx.srv, "/users/hubber/followers", "token "+hubber)
	followers := decodeArray(t, body)
	if len(followers) != 1 || followers[0]["login"] != "octocat" {
		t.Fatalf("followers = %v, want [octocat]", followers)
	}

	// Following oneself is 422.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/user/following/octocat", octo, "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("self-follow %d, want 422, body %s", resp.StatusCode, body)
	}

	// Unfollow: 204, and the check returns to 404.
	resp, _ = authedSend(t, fx.srv, http.MethodDelete, "/user/following/hubber", octo, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unfollow %d, want 204", resp.StatusCode)
	}
	resp, _ = authedGet(t, fx.srv, "/user/following/hubber", "token "+octo)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-unfollow check %d, want 404", resp.StatusCode)
	}
}

// TestFollowUnknownUser confirms following a nonexistent login 404s.
func TestFollowUnknownUser(t *testing.T) {
	fx := repoServer(t)
	octo := fx.userToken(t, fx.ownerPK)
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/user/following/ghost", octo, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("follow ghost %d, want 404, body %s", resp.StatusCode, body)
	}
}
