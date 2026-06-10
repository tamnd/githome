---
title: "Code Review"
description: "pull request reviews and inline comments via the githome REST and GraphQL APIs"
weight: 50
---

## Review lifecycle

A review moves through a simple state machine. When you create a review without specifying an event, it starts as `PENDING` (a draft visible only to the author). Submitting the review transitions it to one of three terminal states: `APPROVED`, `CHANGES_REQUESTED`, or `COMMENTED`.

```
PENDING  -->  APPROVED
          -->  CHANGES_REQUESTED
          -->  COMMENTED
```

A `PENDING` review can be deleted before submission. Terminal reviews can only be dismissed, not edited.

The PR itself carries a derived `review_decision` field:

- `APPROVED` if the latest non-dismissed review from every required reviewer is an approval.
- `CHANGES_REQUESTED` if any required reviewer has a non-dismissed `CHANGES_REQUESTED` review.
- `REVIEW_REQUIRED` if the branch protection rule requires review and none has been submitted.

## Create a review

`POST /repos/{owner}/{repo}/pulls/{number}/reviews`

Omit `event` to create a `PENDING` draft. Include `event` to submit immediately.

Request body:

| Field | Type | Description |
|---|---|---|
| `body` | string | Top-level review comment. |
| `event` | string | `APPROVE`, `REQUEST_CHANGES`, or `COMMENT`. Omit for draft. |
| `commit_id` | string | SHA of the commit being reviewed. Defaults to the PR head. |
| `comments` | array | Inline comments to attach. See fields below. |

Each entry in `comments`:

| Field | Type | Description |
|---|---|---|
| `path` | string | File path relative to repo root. |
| `line` | integer | Line number in the new file (right side). |
| `side` | string | `RIGHT` (new) or `LEFT` (old). Defaults to `RIGHT`. |
| `position` | integer | Diff hunk position (GitHub legacy; prefer `line`+`side`). |
| `body` | string | Comment text. |

Submit an approval with two inline comments:

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/reviews \
  -d '{
    "body": "Looks good overall. Two minor nits.",
    "event": "APPROVE",
    "commit_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
    "comments": [
      {
        "path": "internal/server/handler.go",
        "line": 42,
        "side": "RIGHT",
        "body": "Prefer `errors.As` over a type assertion here."
      },
      {
        "path": "internal/server/handler.go",
        "line": 61,
        "side": "RIGHT",
        "body": "This branch is unreachable; remove it."
      }
    ]
  }'
```

Create a `PENDING` draft (no `event`):

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/reviews \
  -d '{"body": "Still reviewing..."}'
```

### Review object

```json
{
  "id": 101,
  "node_id": "PRR_kgDOB...",
  "user": {
    "login": "bob",
    "id": 2,
    "node_id": "U_kgDOB...",
    "avatar_url": "https://git.example.com/avatars/u/2",
    "type": "User"
  },
  "body": "Looks good overall. Two minor nits.",
  "state": "APPROVED",
  "html_url": "https://git.example.com/alice/myrepo/pull/7#pullrequestreview-101",
  "commit_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "submitted_at": "2026-06-10T09:12:00Z",
  "_links": {
    "html": { "href": "https://git.example.com/alice/myrepo/pull/7#pullrequestreview-101" },
    "pull_request": { "href": "https://git.example.com/repos/alice/myrepo/pulls/7" }
  }
}
```

## List reviews

`GET /repos/{owner}/{repo}/pulls/{number}/reviews`

Returns an array of review objects ordered by submission time. Pending reviews submitted by the authenticated user are included.

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/reviews
```

## Get a review

`GET /repos/{owner}/{repo}/pulls/{number}/reviews/{id}`

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/reviews/101
```

## Submit a pending review

`POST /repos/{owner}/{repo}/pulls/{number}/reviews/{id}/events`

Transitions a `PENDING` review to a terminal state. The `body` field is optional here; it overwrites the draft body if provided.

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/reviews/98/events \
  -d '{"event": "REQUEST_CHANGES", "body": "Please address the two comments above."}'
```

Valid `event` values: `APPROVE`, `REQUEST_CHANGES`, `COMMENT`.

## Delete a pending review

`DELETE /repos/{owner}/{repo}/pulls/{number}/reviews/{id}`

Only works on `PENDING` reviews. Returns `200` with the deleted review object. Returns `422` if the review is already in a terminal state.

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/reviews/98
```

## Dismiss a review

`PUT /repos/{owner}/{repo}/pulls/{number}/reviews/{id}/dismissals`

Dismisses an `APPROVED` or `CHANGES_REQUESTED` review. Requires write access to the repository. The dismissed review no longer counts toward `review_decision`.

```bash
curl -s -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/reviews/101/dismissals \
  -d '{"message": "Stale review: rebased onto main, logic changed."}'
```

## Inline comments

Inline (review) comments attach to a specific line of a specific file in the diff.

### Create an inline comment

`POST /repos/{owner}/{repo}/pulls/{number}/comments`

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/comments \
  -d '{
    "body": "Use `context.WithTimeout` instead of a raw deadline.",
    "path": "cmd/serve/main.go",
    "line": 88,
    "side": "RIGHT",
    "commit_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
  }'
```

### Reply to an existing comment

Supply `in_reply_to` with the parent comment ID. The `path`, `line`, and `commit_id` fields are ignored when replying; they are inherited from the parent thread.

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/pulls/7/comments \
  -d '{
    "body": "Good point, fixed in the next commit.",
    "in_reply_to": 55
  }'
```

### Review comment object

```json
{
  "id": 55,
  "node_id": "PRRC_kgDOB...",
  "pull_request_review_id": 101,
  "diff_hunk": "@@ -38,6 +38,8 @@ func serve(ctx context.Context) error {",
  "path": "cmd/serve/main.go",
  "position": 4,
  "original_position": 4,
  "commit_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "original_commit_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "line": 88,
  "original_line": 88,
  "side": "RIGHT",
  "in_reply_to_id": null,
  "body": "Use `context.WithTimeout` instead of a raw deadline.",
  "created_at": "2026-06-10T09:15:00Z",
  "updated_at": "2026-06-10T09:15:00Z",
  "html_url": "https://git.example.com/alice/myrepo/pull/7#discussion_r55",
  "user": {
    "login": "bob",
    "id": 2,
    "type": "User"
  },
  "_links": {
    "self": { "href": "https://git.example.com/repos/alice/myrepo/pulls/comments/55" },
    "html": { "href": "https://git.example.com/alice/myrepo/pull/7#discussion_r55" },
    "pull_request": { "href": "https://git.example.com/repos/alice/myrepo/pulls/7" }
  }
}
```

### List inline comments

`GET /repos/{owner}/{repo}/pulls/{number}/comments`

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "https://git.example.com/repos/alice/myrepo/pulls/7/comments?per_page=50"
```

### Update an inline comment

`PATCH /repos/{owner}/{repo}/pulls/comments/{id}`

```bash
curl -s -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/repos/alice/myrepo/pulls/comments/55 \
  -d '{"body": "Updated: use `context.WithDeadlineCause` in Go 1.21+."}'
```

### Delete an inline comment

`DELETE /repos/{owner}/{repo}/pulls/comments/{id}`

Returns `204 No Content`.

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/pulls/comments/55
```

## Review decision

The PR object includes `review_decision` as a derived field. You do not set it directly; githome recomputes it after each review operation.

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/repos/alice/myrepo/pulls/7 \
  | jq '.review_decision'
# "APPROVED"
```

Possible values: `"APPROVED"`, `"CHANGES_REQUESTED"`, `"REVIEW_REQUIRED"`, or `null` (no reviews yet and none required).

## GraphQL mutations

All review mutations are available over the GraphQL endpoint at `POST /api/graphql`.

**Create a review:**

```graphql
mutation {
  addPullRequestReview(input: {
    pullRequestId: "PR_kgDOB..."
    event: APPROVE
    body: "LGTM"
    comments: [
      { path: "go.mod", line: 3, body: "Bump to go 1.23." }
    ]
  }) {
    pullRequestReview {
      id
      state
      submittedAt
    }
  }
}
```

**Submit a pending review:**

```graphql
mutation {
  submitPullRequestReview(input: {
    pullRequestReviewId: "PRR_kgDOB..."
    event: REQUEST_CHANGES
    body: "Please fix the nits before merging."
  }) {
    pullRequestReview {
      id
      state
    }
  }
}
```

**Delete a pending review:**

```graphql
mutation {
  deletePullRequestReview(input: {
    pullRequestReviewId: "PRR_kgDOB..."
  }) {
    pullRequestReview { id }
  }
}
```

**Add a standalone inline comment:**

```graphql
mutation {
  addPullRequestReviewComment(input: {
    pullRequestId: "PR_kgDOB..."
    commitOID: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
    path: "cmd/serve/main.go"
    line: 88
    body: "Consider context.WithTimeout here."
  }) {
    comment { id body }
  }
}
```

**Resolve and unresolve a thread:**

```graphql
mutation {
  resolveReviewThread(input: { threadId: "PRRT_kgDOB..." }) {
    thread { id isResolved }
  }
}

mutation {
  unresolveReviewThread(input: { threadId: "PRRT_kgDOB..." }) {
    thread { id isResolved }
  }
}
```
