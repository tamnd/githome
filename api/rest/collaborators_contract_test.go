package rest

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestCollaboratorAddInvitation covers the PUT contract: a brand new grant is
// 201 with the invitation object (octokit reads response.data.id), repeating
// the call is the already-a-collaborator 204, and naming the owner is a 204
// no-op. A bogus permission value is the structured 422.
func TestCollaboratorAddInvitation(t *testing.T) {
	fx := repoServer(t)
	fx.addUser(t, "hubber")

	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/collaborators/hubber", fx.token,
		`{"permission":"push"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add status %d, want 201, body %s", resp.StatusCode, body)
	}
	inv := decodeObject(t, body)
	if _, ok := inv["id"].(float64); !ok {
		t.Fatalf("invitation id = %v, want a number", inv["id"])
	}
	invitee, _ := inv["invitee"].(map[string]any)
	if invitee == nil || invitee["login"] != "hubber" {
		t.Fatalf("invitee = %v", inv["invitee"])
	}
	inviter, _ := inv["inviter"].(map[string]any)
	if inviter == nil || inviter["login"] != "octocat" {
		t.Fatalf("inviter = %v", inv["inviter"])
	}
	repository, _ := inv["repository"].(map[string]any)
	if repository == nil || repository["full_name"] != "octocat/hello" {
		t.Fatalf("repository = %v", inv["repository"])
	}
	if inv["permissions"] != "write" {
		t.Fatalf("permissions = %v, want write", inv["permissions"])
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/collaborators/hubber", fx.token,
		`{"permission":"admin"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("repeat add status %d, want 204, body %s", resp.StatusCode, body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/collaborators/octocat", fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner add status %d, want 204, body %s", resp.StatusCode, body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/collaborators/hubber", fx.token,
		`{"permission":"owner"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad permission status %d, want 422, body %s", resp.StatusCode, body)
	}
}

// TestCollaboratorCheckAndList covers the 204 existence check and the list:
// the owner is always present as admin, a granted user appears with their
// role_name and permission booleans, and removal returns the check to 404.
func TestCollaboratorCheckAndList(t *testing.T) {
	fx := repoServer(t)
	fx.addUser(t, "hubber")

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/collaborators/octocat", "token "+fx.token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner check status %d, want 204, body %s", resp.StatusCode, body)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/collaborators/hubber", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-collaborator check status %d, want 404, body %s", resp.StatusCode, body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/collaborators/hubber", fx.token,
		`{"permission":"pull"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/collaborators/hubber", "token "+fx.token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("collaborator check status %d, want 204, body %s", resp.StatusCode, body)
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/collaborators", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d, body %s", resp.StatusCode, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0]["login"] != "octocat" || list[1]["login"] != "hubber" {
		t.Fatalf("list = %s", body)
	}
	if list[0]["role_name"] != "admin" || list[1]["role_name"] != "read" {
		t.Fatalf("role names = %v, %v", list[0]["role_name"], list[1]["role_name"])
	}
	perms, _ := list[1]["permissions"].(map[string]any)
	if perms == nil || perms["pull"] != true || perms["push"] != false {
		t.Fatalf("hubber permissions = %v", list[1]["permissions"])
	}

	resp, body = authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/collaborators/hubber", fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/collaborators/hubber", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete check status %d, want 404, body %s", resp.StatusCode, body)
	}
}

// TestCollaboratorPermissionGet covers the permission endpoint: coarse
// permission, role_name, and the nested user object carrying its own
// permissions block, for the owner, a granted collaborator, and a user with
// no grant.
func TestCollaboratorPermissionGet(t *testing.T) {
	fx := repoServer(t)
	fx.addUser(t, "hubber")

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/collaborators/octocat/permission", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner permission status %d, body %s", resp.StatusCode, body)
	}
	obj := decodeObject(t, body)
	if obj["permission"] != "admin" || obj["role_name"] != "admin" {
		t.Fatalf("owner permission = %v role %v", obj["permission"], obj["role_name"])
	}
	user, _ := obj["user"].(map[string]any)
	if user == nil || user["login"] != "octocat" || user["role_name"] != "admin" {
		t.Fatalf("owner user = %v", obj["user"])
	}
	uperms, _ := user["permissions"].(map[string]any)
	if uperms == nil || uperms["admin"] != true {
		t.Fatalf("owner user permissions = %v", user["permissions"])
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/collaborators/hubber", fx.token,
		`{"permission":"triage"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/collaborators/hubber/permission", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("granted permission status %d, body %s", resp.StatusCode, body)
	}
	obj = decodeObject(t, body)
	if obj["permission"] != "read" || obj["role_name"] != "triage" {
		t.Fatalf("granted permission = %v role %v", obj["permission"], obj["role_name"])
	}

	fx.addUser(t, "stranger")
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/collaborators/stranger/permission", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stranger permission status %d, body %s", resp.StatusCode, body)
	}
	obj = decodeObject(t, body)
	if obj["permission"] != "none" || obj["role_name"] != "none" {
		t.Fatalf("stranger permission = %v role %v", obj["permission"], obj["role_name"])
	}
}
