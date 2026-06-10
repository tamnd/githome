---
title: "Pull Requests API"
description: "create, review, and merge pull requests with inline comments, reviews, and diff retrieval"
weight: 40
---

## Authentication

All write operations require a bearer token:

```
Authorization: Bearer <token>
```

Set `Accept: application/vnd.github+json` on every request.

## Pull request object

A representative PR response:

```json
{
  "id": 88,
  "node_id": "PR_kgDOBc3xAA",
  "number": 17,
  "title": "feat: add sparse checkout support",
  "body": "Implements sparse checkout via the cone pattern mode.\n\nCloses #42.",
  "state": "open",
  "draft": false,
  "locked": false,
  "mergeable": true,
  "merge_commit_sha": null,
  "merged": false,
  "merged_at": null,
  "merged_by": null,
  "mergeStateStatus": "CLEAN",
  "maintainer_can_modify": true,
  "head": {
    "label": "alice:feat/sparse-checkout",
    "ref": "feat/sparse-checkout",
    "sha": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
    "repo": {
      "full_name": "alice/myrepo"
    }
  },
  "base": {
    "label": "alice:main",
    "ref": "main",
    "sha": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
    "repo": {
      "full_name": "alice/myrepo"
    }
  },
  "user": {
    "login": "alice",
    "id": 5,
    "node_id": "U_kgDOAA5",
    "type": "User"
  },
  "assignees": [],
  "labels": [
    {
      "id": 10,
      "node_id": "LA_kgDOAA10",
      "name": "enhancement",
      "color": "a2eeef",
      "description": ""
    }
  ],
  "milestone": null,
  "commits": 3,
  "additions": 120,
  "deletions": 8,
  "changed_files": 5,
  "created_at": "2026-06-01T10:00:00Z",
  "updated_at": "2026-06-09T14:30:00Z",
  "closed_at": null,
  "url": "https://git.example.com/api/v3/repos/alice/myrepo/pulls/17",
  "html_url": "https://git.example.com/alice/myrepo/pull/17",
  "diff_url": "https://git.example.com/alice/myrepo/pull/17.diff",
  "patch_url": "https://git.example.com/alice/myrepo/pull/17.patch"
}
```

### mergeStateStatus values

| Value | Meaning |
|---|---|
| `CLEAN` | All checks pass; can merge immediately |
| `DIRTY` | Merge conflict exists |
| `BEHIND` | Head is behind base; rebase or merge to update |
| `BLOCKED` | Branch protection rule prevents merge |
| `DRAFT` | PR is in draft state |
| `HAS_HOOKS` | Merge is waiting on pre-receive hooks |
| `UNSTABLE` | Some checks are failing or pending |
| `UNKNOWN` | Status cannot be determined yet |

## Create a pull request

```
POST /repos/{owner}/{repo}/pulls
```

Request body fields:

| Field | Type | Required | Description |
|---|---|---|---|
| `title` | string | yes | PR title |
| `head` | string | yes | Branch (or `user:branch` for cross-fork) to merge from |
| `base` | string | yes | Branch to merge into |
| `body` | string | no | Markdown description |
| `draft` | boolean | no | Open as draft (default `false`) |
| `maintainer_can_modify` | boolean | no | Allow maintainers to push to head branch |

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/pulls \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "feat: add sparse checkout support",
    "head": "feat/sparse-checkout",
    "base": "main",
    "body": "Implements sparse checkout via the cone pattern mode.\n\nCloses #42.",
    "draft": false,
    "maintainer_can_modify": true
  }'
```

Returns `201 Created` with the full PR object.

To open a PR from a fork, use the `user:branch` form for `head`:

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/pulls \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "fix: handle empty tree on first push",
    "head": "bob:fix/empty-tree",
    "base": "main"
  }'
```

### Draft pull requests

Set `"draft": true` when creating. Draft PRs report `mergeStateStatus: DRAFT` and cannot be merged until converted to ready.

To convert a draft to ready via GraphQL:

```graphql
mutation {
  convertPullRequestToDraft(input: { pullRequestId: "PR_kgDOBc3xAA" }) {
    pullRequest {
      number
      isDraft
    }
  }
}
```

```bash
curl -X POST https://git.example.com/api/graphql \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query":"mutation { convertPullRequestToDraft(input:{pullRequestId:\"PR_kgDOBc3xAA\"}) { pullRequest { number isDraft } } }"}'
```

## List pull requests

```
GET /repos/{owner}/{repo}/pulls
```

Query parameters:

| Parameter | Values | Default |
|---|---|---|
| `state` | `open`, `closed`, `all` | `open` |
| `sort` | `created`, `updated`, `popularity`, `long-running` | `created` |
| `direction` | `asc`, `desc` | `desc` |
| `base` | base branch name | |
| `head` | `user:branch` filter | |
| `per_page` | 1-100 | `30` |
| `page` | integer | `1` |

```bash
# all open PRs targeting main
curl "https://git.example.com/repos/alice/myrepo/pulls?state=open&base=main&sort=updated&direction=desc" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

```bash
# merged PRs from a fork user
curl "https://git.example.com/repos/alice/myrepo/pulls?state=closed&head=bob:fix/empty-tree" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

## Get a pull request

```
GET /repos/{owner}/{repo}/pulls/{number}
```

```bash
curl https://git.example.com/repos/alice/myrepo/pulls/17 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

The `mergeable` field is computed asynchronously. On first fetch after a push it may be `null`; retry after a short delay.

## Update a pull request

```
PATCH /repos/{owner}/{repo}/pulls/{number}
```

All fields are optional:

```bash
# retitle and change base branch
curl -X PATCH https://git.example.com/repos/alice/myrepo/pulls/17 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "feat: add sparse checkout (cone mode only)",
    "base": "develop",
    "maintainer_can_modify": false
  }'
```

```bash
# close without merging
curl -X PATCH https://git.example.com/repos/alice/myrepo/pulls/17 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"state": "closed"}'
```

## Merge a pull request

```
PUT /repos/{owner}/{repo}/pulls/{number}/merge
```

Request body fields:

| Field | Type | Description |
|---|---|---|
| `commit_title` | string | Title for the merge commit (merge/squash) |
| `commit_message` | string | Extra message for the merge commit |
| `merge_method` | string | `merge`, `squash`, or `rebase` (default `merge`) |
| `sha` | string | Expected head SHA; fails if head has moved |

```bash
# merge commit
curl -X PUT https://git.example.com/repos/alice/myrepo/pulls/17/merge \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "commit_title": "feat: add sparse checkout support (#17)",
    "merge_method": "merge",
    "sha": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
  }'
```

```bash
# squash into one commit
curl -X PUT https://git.example.com/repos/alice/myrepo/pulls/17/merge \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "commit_title": "feat: sparse checkout (squashed)",
    "merge_method": "squash"
  }'
```

```bash
# rebase onto base
curl -X PUT https://git.example.com/repos/alice/myrepo/pulls/17/merge \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"merge_method": "rebase"}'
```

Successful response:

```json
{
  "sha": "f1e2d3c4b5a6f1e2d3c4b5a6f1e2d3c4b5a6f1e2",
  "merged": true,
  "message": "Pull Request successfully merged"
}
```

The server returns `405 Method Not Allowed` if `mergeStateStatus` is not `CLEAN`, or `409 Conflict` if the provided `sha` does not match the current head.

## Files changed

```
GET /repos/{owner}/{repo}/pulls/{number}/files
```

```bash
curl https://git.example.com/repos/alice/myrepo/pulls/17/files \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Each item in the response array:

```json
{
  "sha": "abc123",
  "filename": "pkg/git/sparse.go",
  "status": "added",
  "additions": 98,
  "deletions": 0,
  "changes": 98,
  "blob_url": "https://git.example.com/alice/myrepo/blob/a1b2c3/pkg/git/sparse.go",
  "raw_url": "https://git.example.com/alice/myrepo/raw/a1b2c3/pkg/git/sparse.go",
  "patch": "@@ -0,0 +1,98 @@\n+package git\n+..."
}
```

`status` values: `added`, `modified`, `removed`, `renamed`, `copied`, `changed`, `unchanged`.

## Commits in a pull request

```
GET /repos/{owner}/{repo}/pulls/{number}/commits
```

```bash
curl https://git.example.com/repos/alice/myrepo/pulls/17/commits \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Returns an array of commit objects in chronological order, same shape as `GET /repos/{owner}/{repo}/commits/{sha}`.

## Retrieve the diff or patch

Pass a content-type accept header to get the raw diff or patch file instead of JSON:

```bash
# unified diff
curl https://git.example.com/repos/alice/myrepo/pulls/17 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github.diff" \
  > pr-17.diff
```

```bash
# git-format-patch compatible patch
curl https://git.example.com/repos/alice/myrepo/pulls/17 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github.patch" \
  > pr-17.patch
```

## Reviews

### Create a review

```
POST /repos/{owner}/{repo}/pulls/{number}/reviews
```

| Field | Type | Description |
|---|---|---|
| `commit_id` | string | Commit SHA the review is against (defaults to head) |
| `body` | string | Top-level review comment |
| `event` | string | `APPROVE`, `REQUEST_CHANGES`, or `COMMENT` |
| `comments` | array | Inline review comments (see below) |

Each inline comment in `comments`:

| Field | Type | Description |
|---|---|---|
| `path` | string | File path relative to repo root |
| `position` | integer | Line position in the diff (deprecated, prefer `line`) |
| `line` | integer | Line number in the file |
| `side` | string | `LEFT` or `RIGHT` (default `RIGHT`) |
| `body` | string | Comment text |

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/pulls/17/reviews \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "body": "Overall looks good. One nit inline.",
    "event": "APPROVE",
    "comments": [
      {
        "path": "pkg/git/sparse.go",
        "line": 42,
        "side": "RIGHT",
        "body": "This allocates on every call. Consider caching the result."
      }
    ]
  }'
```

### Submit a pending review

A review created without an `event` field is saved as `PENDING`. Submit it with:

```
POST /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/events
```

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/pulls/17/reviews/55/events \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"body": "Looks good after the nit is addressed.", "event": "APPROVE"}'
```

### List reviews

```
GET /repos/{owner}/{repo}/pulls/{number}/reviews
```

```bash
curl https://git.example.com/repos/alice/myrepo/pulls/17/reviews \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Get a review

```
GET /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}
```

```bash
curl https://git.example.com/repos/alice/myrepo/pulls/17/reviews/55 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Delete a review

Only `PENDING` reviews can be deleted.

```
DELETE /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}
```

```bash
curl -X DELETE https://git.example.com/repos/alice/myrepo/pulls/17/reviews/55 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Dismiss a review

```
PUT /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/dismissals
```

```bash
curl -X PUT https://git.example.com/repos/alice/myrepo/pulls/17/reviews/55/dismissals \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"message": "Addressed in follow-up commit."}'
```

## Inline review comments

Inline comments can be created independently of a review.

### Create an inline comment

```
POST /repos/{owner}/{repo}/pulls/{number}/comments
```

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/pulls/17/comments \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "body": "Consider extracting this into a helper.",
    "commit_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
    "path": "pkg/git/sparse.go",
    "line": 67,
    "side": "RIGHT"
  }'
```

Returns `201 Created`.

### List inline comments

```
GET /repos/{owner}/{repo}/pulls/{number}/comments
```

```bash
curl "https://git.example.com/repos/alice/myrepo/pulls/17/comments?sort=created&direction=asc" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Get, update, delete an inline comment

```bash
# get
curl https://git.example.com/repos/alice/myrepo/pulls/comments/202 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"

# update
curl -X PATCH https://git.example.com/repos/alice/myrepo/pulls/comments/202 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"body": "Consider extracting this into a package-level helper."}'

# delete
curl -X DELETE https://git.example.com/repos/alice/myrepo/pulls/comments/202 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

## Check status and mergeability

`mergeStateStatus` in the PR object reflects both branch protection rules and CI check results. To check whether a PR is mergeable before attempting:

```bash
STATUS=$(curl -s https://git.example.com/repos/alice/myrepo/pulls/17 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" | jq -r '.mergeStateStatus')

if [ "$STATUS" = "CLEAN" ]; then
  echo "ready to merge"
else
  echo "cannot merge: $STATUS"
fi
```

Check runs that feed into `mergeStateStatus` are accessible at:

```bash
curl https://git.example.com/repos/alice/myrepo/commits/a1b2c3d4/check-runs \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

## Search pull requests

The search API covers PRs via the `is:pr` qualifier:

```bash
# open PRs with failing checks
curl "https://git.example.com/search/issues?q=is:pr+is:open+repo:alice/myrepo+status:failure" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```
