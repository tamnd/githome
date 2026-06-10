---
title: "Issues API"
description: "create, read, update, and close issues with labels, milestones, assignees, and reactions"
weight: 30
---

## Authentication

All write operations require a bearer token. Pass it as:

```
Authorization: Bearer <token>
```

or the legacy form `Authorization: token <token>`. Set `Accept: application/vnd.github+json` on every request.

## Issue object

A representative issue response:

```json
{
  "id": 1,
  "node_id": "I_kgDOBc3xAA",
  "number": 42,
  "title": "panic when repo has no commits",
  "body": "Steps to reproduce:\n1. Create a bare repo\n2. Clone it\n3. Visit /owner/repo\n\nStack trace attached.",
  "state": "open",
  "state_reason": null,
  "locked": false,
  "user": {
    "login": "alice",
    "id": 5,
    "node_id": "U_kgDOAA5",
    "avatar_url": "https://git.example.com/avatars/alice",
    "type": "User"
  },
  "assignees": [
    {
      "login": "bob",
      "id": 7,
      "node_id": "U_kgDOAA7",
      "type": "User"
    }
  ],
  "labels": [
    {
      "id": 3,
      "node_id": "LA_kgDOAA3",
      "name": "bug",
      "color": "d73a4a",
      "description": "Something isn't working",
      "default": true
    }
  ],
  "milestone": {
    "id": 2,
    "node_id": "MI_kgDOAA2",
    "number": 1,
    "title": "v1.0",
    "state": "open",
    "due_on": "2026-09-01T00:00:00Z"
  },
  "comments": 3,
  "created_at": "2026-05-10T09:14:22Z",
  "updated_at": "2026-06-01T14:05:33Z",
  "closed_at": null,
  "url": "https://git.example.com/api/v3/repos/alice/myrepo/issues/42",
  "html_url": "https://git.example.com/alice/myrepo/issues/42",
  "repository_url": "https://git.example.com/api/v3/repos/alice/myrepo"
}
```

## Create an issue

```
POST /repos/{owner}/{repo}/issues
```

Request body fields:

| Field | Type | Required | Description |
|---|---|---|---|
| `title` | string | yes | Issue title |
| `body` | string | no | Markdown body |
| `labels` | array of strings | no | Label names to attach |
| `assignees` | array of strings | no | Logins to assign |
| `milestone` | integer | no | Milestone number |

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/issues \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "panic when repo has no commits",
    "body": "Steps to reproduce:\n1. Create a bare repo\n2. Clone it",
    "labels": ["bug"],
    "assignees": ["bob"],
    "milestone": 1
  }'
```

Returns `201 Created` with the full issue object.

## List issues

```
GET /repos/{owner}/{repo}/issues
```

Query parameters:

| Parameter | Values | Default |
|---|---|---|
| `state` | `open`, `closed`, `all` | `open` |
| `sort` | `created`, `updated`, `comments` | `created` |
| `direction` | `asc`, `desc` | `desc` |
| `labels` | comma-separated label names | |
| `assignee` | login, `none`, `*` | |
| `milestone` | milestone number, `none`, `*` | |
| `since` | ISO 8601 timestamp | |
| `per_page` | 1-100 | `30` |
| `page` | integer | `1` |

```bash
# open bugs assigned to alice, newest first
curl "https://git.example.com/repos/alice/myrepo/issues?state=open&labels=bug&assignee=alice&sort=created&direction=desc" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

```bash
# issues updated after a specific timestamp
curl "https://git.example.com/repos/alice/myrepo/issues?state=all&since=2026-01-01T00:00:00Z&per_page=100" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Returns `200 OK` with an array of issue objects. Pagination links are in the `Link` response header.

## Get a single issue

```
GET /repos/{owner}/{repo}/issues/{number}
```

```bash
curl https://git.example.com/repos/alice/myrepo/issues/42 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

## Update an issue

```
PATCH /repos/{owner}/{repo}/issues/{number}
```

All fields are optional. Only provided fields are changed.

```bash
# close an issue as completed
curl -X PATCH https://git.example.com/repos/alice/myrepo/issues/42 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"state": "closed", "state_reason": "completed"}'
```

```bash
# close as not planned
curl -X PATCH https://git.example.com/repos/alice/myrepo/issues/42 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"state": "closed", "state_reason": "not_planned"}'
```

```bash
# reopen
curl -X PATCH https://git.example.com/repos/alice/myrepo/issues/42 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"state": "open", "state_reason": "reopened"}'
```

```bash
# retitle, reassign, relabel in one call
curl -X PATCH https://git.example.com/repos/alice/myrepo/issues/42 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "panic when repo has no commits (reproducible)",
    "assignees": ["carol"],
    "labels": ["bug", "good first issue"]
  }'
```

The `state_reason` field accepts `completed`, `not_planned`, or `reopened`. It is null when the issue is open with no prior close reason.

## Comments

### List comments on an issue

```
GET /repos/{owner}/{repo}/issues/{number}/comments
```

```bash
curl "https://git.example.com/repos/alice/myrepo/issues/42/comments?per_page=30&page=1" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Create a comment

```
POST /repos/{owner}/{repo}/issues/{number}/comments
```

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/issues/42/comments \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"body": "Confirmed on main. The nil pointer is in `pkg/web/tree.go:84`."}'
```

Returns `201 Created`.

### Get a comment

```
GET /repos/{owner}/{repo}/issues/comments/{id}
```

```bash
curl https://git.example.com/repos/alice/myrepo/issues/comments/101 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Update a comment

```
PATCH /repos/{owner}/{repo}/issues/comments/{id}
```

```bash
curl -X PATCH https://git.example.com/repos/alice/myrepo/issues/comments/101 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"body": "Confirmed on main and 0.9.x."}'
```

### Delete a comment

```
DELETE /repos/{owner}/{repo}/issues/comments/{id}
```

```bash
curl -X DELETE https://git.example.com/repos/alice/myrepo/issues/comments/101 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Returns `204 No Content`.

## Labels

### List labels in a repo

```
GET /repos/{owner}/{repo}/labels
```

```bash
curl https://git.example.com/repos/alice/myrepo/labels \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Create a label

```
POST /repos/{owner}/{repo}/labels
```

`color` is a 6-character hex string without the `#` prefix.

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/labels \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "performance",
    "color": "0075ca",
    "description": "Relates to runtime or memory performance"
  }'
```

Returns `201 Created`.

### List labels on an issue

```
GET /repos/{owner}/{repo}/issues/{number}/labels
```

```bash
curl https://git.example.com/repos/alice/myrepo/issues/42/labels \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Add labels to an issue

```
POST /repos/{owner}/{repo}/issues/{number}/labels
```

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/issues/42/labels \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"labels": ["performance", "help wanted"]}'
```

### Remove a label from an issue

```
DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}
```

```bash
curl -X DELETE https://git.example.com/repos/alice/myrepo/issues/42/labels/performance \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Returns `200 OK` with the remaining labels array.

## Milestones

### List milestones

```
GET /repos/{owner}/{repo}/milestones
```

```bash
curl "https://git.example.com/repos/alice/myrepo/milestones?state=open&sort=due_on&direction=asc" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Create a milestone

```
POST /repos/{owner}/{repo}/milestones
```

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/milestones \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "v1.0",
    "description": "First stable release",
    "due_on": "2026-09-01T00:00:00Z",
    "state": "open"
  }'
```

Returns `201 Created` with the milestone object, including `open_issues` and `closed_issues` counts.

## Assignees

Assignees are set via the `assignees` field when creating or updating an issue. To list users eligible for assignment in a repo:

```
GET /repos/{owner}/{repo}/assignees
```

```bash
curl https://git.example.com/repos/alice/myrepo/assignees \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

## Reactions

### List reactions on an issue

```
GET /repos/{owner}/{repo}/issues/{number}/reactions
```

```bash
curl https://git.example.com/repos/alice/myrepo/issues/42/reactions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

### Add a reaction

```
POST /repos/{owner}/{repo}/issues/{number}/reactions
```

The `content` field accepts one of: `+1`, `-1`, `laugh`, `hooray`, `confused`, `heart`, `rocket`, `eyes`.

```bash
curl -X POST https://git.example.com/repos/alice/myrepo/issues/42/reactions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"content": "+1"}'
```

Returns `201 Created` if the reaction is new, or `200 OK` if the authenticated user already reacted with the same content.

```json
{
  "id": 55,
  "node_id": "RE_kgDOAA3z",
  "content": "+1",
  "user": {
    "login": "carol",
    "id": 9,
    "type": "User"
  },
  "created_at": "2026-06-10T08:22:10Z"
}
```

## Search issues

Use the search API to query across repos:

```
GET /search/issues?q={query}
```

```bash
# open bugs in a specific repo
curl "https://git.example.com/search/issues?q=panic+label:bug+repo:alice/myrepo+state:open" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Returns `{"total_count": N, "incomplete_results": false, "items": [...]}`.
