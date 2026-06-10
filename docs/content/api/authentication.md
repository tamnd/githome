---
title: "Authentication"
description: "how to authenticate against the githome REST and GraphQL APIs"
weight: 10
---

githome supports four authentication methods: Personal Access Tokens (PATs),
the OAuth web flow, the OAuth device flow, and GitHub App installation tokens.
All methods produce a token you attach to every request via the
`Authorization` header.

## Request format

```http
GET /user HTTP/1.1
Host: git.example.com
Accept: application/vnd.github+json
Authorization: Bearer ghp_yourtoken
X-GitHub-Api-Version: 2022-11-28
```

Both `Bearer` and `token` prefixes are accepted:

```
Authorization: Bearer ghp_yourtoken
Authorization: token ghp_yourtoken
```

The `Accept` header accepts `application/vnd.github+json` or
`application/json`. The `X-GitHub-Api-Version` header is optional but
recommended for forward compatibility.

## Personal Access Token

A PAT is a long-lived credential tied to a user account. It does not expire
unless you revoke it. Create one from the web UI under
**Settings > Developer settings > Personal access tokens**, or via the `gh`
CLI:

```bash
gh auth login --hostname git.example.com
gh auth token
```

Use it directly in curl:

```bash
export TOKEN=ghp_yourtoken
export HOST=https://git.example.com

curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "$HOST/user"
```

To revoke a PAT, delete it from the web UI or use the REST endpoint:

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "$HOST/authorizations/$AUTH_ID"
```

## OAuth web flow

Use this flow when building a web application that authenticates users on
their behalf.

**Step 1.** Redirect the user to the authorization endpoint:

```
GET /login/oauth/authorize?client_id=CLIENT_ID&redirect_uri=https://yourapp.example.com/callback&state=RANDOM_STATE
```

The user logs in and approves your app. githome redirects back to
`redirect_uri` with `code=AUTH_CODE&state=RANDOM_STATE`.

**Step 2.** Exchange the code for a token:

```bash
curl -s -X POST "$HOST/login/oauth/access_token" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "client_id": "CLIENT_ID",
    "client_secret": "CLIENT_SECRET",
    "code": "AUTH_CODE"
  }'
```

Response:

```
access_token=ghp_xxx&token_type=bearer&scope=repo
```

With `Accept: application/vnd.github+json` the response is JSON:

```json
{
  "access_token": "ghp_xxx",
  "token_type": "bearer",
  "scope": "repo"
}
```

Use `access_token` as your Bearer token from this point forward.

## OAuth device flow

Use this flow for CLI tools and apps that cannot open a browser. It requires
only the `client_id`; no client secret is needed.

**Step 1.** Request a device code:

```bash
curl -s -X POST "$HOST/login/device/code" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"client_id": "CLIENT_ID", "scope": "repo"}'
```

Response:

```json
{
  "device_code": "DEVICE_CODE",
  "user_code": "ABCD-1234",
  "verification_uri": "https://git.example.com/login/device",
  "expires_in": 900,
  "interval": 5
}
```

**Step 2.** Ask the user to visit `verification_uri` and enter `user_code`.

**Step 3.** Poll for the token. Wait at least `interval` seconds between
requests:

```bash
curl -s -X POST "$HOST/login/oauth/access_token" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "client_id": "CLIENT_ID",
    "device_code": "DEVICE_CODE",
    "grant_type": "urn:ietf:params:oauth:grant-type:device_code"
  }'
```

While the user has not yet approved, the response is:

```json
{"error": "authorization_pending"}
```

Once approved:

```json
{
  "access_token": "ghp_xxx",
  "token_type": "bearer",
  "scope": "repo"
}
```

## GitHub App installation token

A GitHub App authenticates as an installation to access repositories. The app
must first be installed on an account or organization.

Exchange an installation token:

```bash
curl -s -X POST "$HOST/app/installations/$INSTALLATION_ID/access_tokens" \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer $APP_JWT"
```

Response:

```json
{
  "token": "ghs_xxx",
  "expires_at": "2026-06-10T01:00:00Z",
  "permissions": {"contents": "read", "metadata": "read"},
  "repository_selection": "selected"
}
```

The returned `token` expires at `expires_at`. Request a new one before it
expires.

List repositories accessible to the installation token:

```bash
curl -s \
  -H "Authorization: Bearer ghs_xxx" \
  -H "Accept: application/vnd.github+json" \
  "$HOST/installation/repositories"
```

## Rate limiting

Every response includes rate limit headers:

| Header | Meaning |
|--------|---------|
| `X-RateLimit-Limit` | Maximum requests per window |
| `X-RateLimit-Remaining` | Requests left in current window |
| `X-RateLimit-Reset` | Unix timestamp when the window resets |

When you exceed the limit, the API returns `429 Too Many Requests`. Check
`X-RateLimit-Reset` and wait before retrying.

```bash
curl -s -I \
  -H "Authorization: Bearer $TOKEN" \
  "$HOST/user" | grep -i ratelimit
```

## Using with the gh CLI

Point the `gh` CLI at your githome instance:

```bash
gh auth login --hostname git.example.com --with-token <<< "$TOKEN"
```

After login, all `gh` commands targeting `git.example.com` use the stored
token automatically:

```bash
gh --hostname git.example.com repo list
gh --hostname git.example.com issue list --repo owner/repo
```
