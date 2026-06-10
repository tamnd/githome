---
title: "Releases"
description: "create and manage releases and release assets via the githome REST API"
weight: 70
---

## Create a release

`POST /repos/{owner}/{repo}/releases`

A release references a git tag. If the tag does not yet exist, githome creates a lightweight tag pointing at `target_commitish` (defaults to the default branch).

Request body:

| Field | Type | Description |
|---|---|---|
| `tag_name` | string | Tag to create or reference. Required. |
| `target_commitish` | string | Branch name or commit SHA the tag points to. Defaults to default branch. |
| `name` | string | Release title. Defaults to `tag_name` if omitted. |
| `body` | string | Release notes (Markdown). |
| `draft` | boolean | If `true`, the release is not published. Defaults to `false`. |
| `prerelease` | boolean | Mark as a pre-release. Defaults to `false`. |
| `make_latest` | string | `"true"`, `"false"`, or `"legacy"`. Controls which release `GET /releases/latest` returns. |

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/releases \
  -d '{
    "tag_name": "v1.2.0",
    "target_commitish": "main",
    "name": "v1.2.0",
    "body": "## Changes\n\n- Add retry logic\n- Fix timeout on slow connections\n",
    "draft": false,
    "prerelease": false,
    "make_latest": "true"
  }'
```

### Release object

```json
{
  "id": 42,
  "node_id": "RL_kgDOB...",
  "tag_name": "v1.2.0",
  "target_commitish": "main",
  "name": "v1.2.0",
  "body": "## Changes\n\n- Add retry logic\n- Fix timeout on slow connections\n",
  "draft": false,
  "prerelease": false,
  "created_at": "2026-06-10T10:00:00Z",
  "published_at": "2026-06-10T10:01:00Z",
  "author": {
    "login": "alice",
    "id": 1,
    "node_id": "U_kgDOA...",
    "type": "User"
  },
  "url": "https://git.example.com/repos/alice/myrepo/releases/42",
  "html_url": "https://git.example.com/alice/myrepo/releases/tag/v1.2.0",
  "assets_url": "https://git.example.com/repos/alice/myrepo/releases/42/assets",
  "upload_url": "https://git.example.com/api/uploads/repos/alice/myrepo/releases/42/assets{?name,label}",
  "tarball_url": "https://git.example.com/repos/alice/myrepo/tarball/v1.2.0",
  "zipball_url": "https://git.example.com/repos/alice/myrepo/zipball/v1.2.0",
  "assets": []
}
```

## List releases

`GET /repos/{owner}/{repo}/releases`

Returns releases ordered by creation date, newest first. Drafts are only visible to users with write access.

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "https://git.example.com/repos/alice/myrepo/releases?per_page=20"
```

## Get the latest release

`GET /repos/{owner}/{repo}/releases/latest`

Returns the most recent non-draft, non-prerelease release. Returns `404` if no such release exists. The `make_latest` field on each release controls what "latest" means when multiple stable releases exist.

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/releases/latest
```

## Get a release by tag

`GET /repos/{owner}/{repo}/releases/tags/{tag}`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/releases/tags/v1.2.0
```

## Get a release by ID

`GET /repos/{owner}/{repo}/releases/{id}`

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/releases/42
```

## Update a release

`PATCH /repos/{owner}/{repo}/releases/{id}`

All fields are optional. Send only what you want to change.

```bash
curl -s -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/releases/42 \
  -d '{
    "name": "v1.2.0 (hotfix)",
    "body": "## Changes\n\n- Add retry logic\n- Fix timeout on slow connections\n- Patch CVE-2026-1234\n",
    "prerelease": false
  }'
```

To publish a draft release, set `"draft": false`.

## Delete a release

`DELETE /repos/{owner}/{repo}/releases/{id}`

Returns `204 No Content`. Deleting a release does not delete the underlying git tag.

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/releases/42
```

## Upload an asset

`POST /api/uploads/repos/{owner}/{repo}/releases/{id}/assets?name={filename}`

githome uses the `/api/uploads/` prefix for binary uploads, matching the GitHub Enterprise upload endpoint pattern. The body is the raw file bytes; set `Content-Type` to the MIME type of the file.

| Query param | Required | Description |
|---|---|---|
| `name` | yes | The asset filename as it will appear in the release. |
| `label` | no | Human-readable label shown instead of the filename. |

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/octet-stream" \
  "https://git.example.com/api/uploads/repos/alice/myrepo/releases/42/assets?name=myrepo-linux-amd64.tar.gz" \
  --data-binary @myrepo-linux-amd64.tar.gz
```

The response is the asset object:

```json
{
  "id": 7,
  "node_id": "RLA_kgDOB...",
  "name": "myrepo-linux-amd64.tar.gz",
  "label": "",
  "state": "uploaded",
  "content_type": "application/octet-stream",
  "size": 4194304,
  "download_count": 0,
  "created_at": "2026-06-10T10:05:00Z",
  "updated_at": "2026-06-10T10:05:00Z",
  "browser_download_url": "https://git.example.com/repos/alice/myrepo/releases/assets/7"
}
```

## List assets

`GET /repos/{owner}/{repo}/releases/{id}/assets`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/releases/42/assets
```

## Delete an asset

`DELETE /repos/{owner}/{repo}/releases/assets/{asset_id}`

Returns `204 No Content`.

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/releases/assets/7
```

## Full workflow example

Create a release, upload two assets, then verify:

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE="https://git.example.com"
REPO="alice/myrepo"
TOKEN="${GITHOME_TOKEN}"

# 1. Create release
RELEASE=$(curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  "$BASE/repos/$REPO/releases" \
  -d '{
    "tag_name": "v1.3.0",
    "target_commitish": "main",
    "name": "v1.3.0",
    "body": "First release with binaries.",
    "draft": true
  }')

RELEASE_ID=$(echo "$RELEASE" | jq -r '.id')
echo "Created release $RELEASE_ID (draft)"

# 2. Upload Linux binary
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/octet-stream" \
  "$BASE/api/uploads/repos/$REPO/releases/$RELEASE_ID/assets?name=myrepo-linux-amd64.tar.gz" \
  --data-binary @dist/myrepo-linux-amd64.tar.gz \
  | jq '.name, .size'

# 3. Upload macOS binary
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/octet-stream" \
  "$BASE/api/uploads/repos/$REPO/releases/$RELEASE_ID/assets?name=myrepo-darwin-arm64.tar.gz" \
  --data-binary @dist/myrepo-darwin-arm64.tar.gz \
  | jq '.name, .size'

# 4. Publish
curl -s -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  "$BASE/repos/$REPO/releases/$RELEASE_ID" \
  -d '{"draft": false, "make_latest": "true"}' \
  | jq '.html_url'
```
