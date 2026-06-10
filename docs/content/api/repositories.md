---
title: "Repositories"
description: "create, read, update, delete, and list repositories via the REST API"
weight: 20
---

All repository endpoints follow the GitHub REST API v3 shape. Replace
`$HOST`, `$TOKEN`, `$OWNER`, and `$REPO` with your values throughout.

```bash
export HOST=https://git.example.com
export TOKEN=ghp_yourtoken
export OWNER=alice
export REPO=myproject
```

## Repository object

A repository response looks like this:

```json
{
  "id": 42,
  "node_id": "R_kgDOAA",
  "name": "myproject",
  "full_name": "alice/myproject",
  "private": false,
  "owner": {
    "login": "alice",
    "id": 7,
    "node_id": "U_kgDA",
    "avatar_url": "https://git.example.com/avatars/7",
    "type": "User",
    "site_admin": false
  },
  "description": "A sample project",
  "fork": false,
  "url": "https://git.example.com/api/v3/repos/alice/myproject",
  "html_url": "https://git.example.com/alice/myproject",
  "clone_url": "https://git.example.com/alice/myproject.git",
  "ssh_url": "git@git.example.com:alice/myproject.git",
  "homepage": "https://myproject.example.com",
  "language": "Go",
  "stargazers_count": 11,
  "watchers_count": 11,
  "forks_count": 2,
  "open_issues_count": 3,
  "default_branch": "main",
  "visibility": "public",
  "has_issues": true,
  "has_wiki": false,
  "pushed_at": "2026-06-09T14:22:00Z",
  "created_at": "2025-01-15T09:00:00Z",
  "updated_at": "2026-06-09T14:22:00Z",
  "topics": ["go", "api"],
  "archived": false,
  "disabled": false
}
```

## Create a repository

Create under the authenticated user:

```bash
curl -s -X POST "$HOST/user/repos" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "myproject",
    "description": "A sample project",
    "private": false,
    "has_issues": true,
    "has_wiki": false,
    "auto_init": true,
    "default_branch": "main"
  }'
```

Create under an organization:

```bash
curl -s -X POST "$HOST/orgs/myorg/repos" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "myproject",
    "private": true
  }'
```

Both return `201 Created` with the full repository object.

## Get a repository

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "$HOST/repos/$OWNER/$REPO"
```

Returns `200 OK` with the repository object. Returns `404 Not Found` if the
repository does not exist or you lack read access.

## Update a repository

```bash
curl -s -X PATCH "$HOST/repos/$OWNER/$REPO" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Updated description",
    "homepage": "https://myproject.example.com",
    "private": true,
    "has_issues": true,
    "has_wiki": false,
    "default_branch": "main"
  }'
```

All fields are optional. Only included fields are updated. Returns `200 OK`
with the updated repository object.

## Delete a repository

```bash
curl -s -X DELETE "$HOST/repos/$OWNER/$REPO" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Returns `204 No Content` on success. This operation is permanent. The
underlying git data is removed from disk.

## List repositories for the authenticated user

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "$HOST/user/repos?type=all&sort=pushed&direction=desc&per_page=30"
```

Query parameters:

| Parameter | Values | Default |
|-----------|--------|---------|
| `type` | `all`, `owner`, `member`, `public`, `private` | `owner` |
| `sort` | `created`, `updated`, `pushed`, `full_name` | `full_name` |
| `direction` | `asc`, `desc` | depends on `sort` |
| `per_page` | 1-100 | 30 |
| `page` | integer | 1 |

Returns an array of repository objects. Pagination links are in the `Link`
response header:

```
Link: <https://git.example.com/user/repos?page=2>; rel="next"
```

## List public repositories for a user

No authentication required for public repositories:

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  "$HOST/users/$OWNER/repos"
```

## Topics

Get topics:

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "$HOST/repos/$OWNER/$REPO/topics"
```

Response:

```json
{"names": ["go", "api", "rest"]}
```

Replace topics (this is a full replacement, not a merge):

```bash
curl -s -X PUT "$HOST/repos/$OWNER/$REPO/topics" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"names": ["go", "api", "rest", "githome"]}'
```

Returns `200 OK` with the updated `{"names": [...]}` object. Topic names must
be lowercase, start with a letter or number, and contain only letters,
numbers, and hyphens. Maximum 20 topics per repository.

## Fork a repository

Fork to the authenticated user's namespace:

```bash
curl -s -X POST "$HOST/repos/$OWNER/$REPO/forks" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json"
```

Fork into an organization:

```bash
curl -s -X POST "$HOST/repos/$OWNER/$REPO/forks" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"organization": "myorg"}'
```

Returns `202 Accepted` while the fork is being created. The fork appears as a
full repository object with `"fork": true` and the `parent` field pointing to
the source repository.

## Transfer a repository

Transfer ownership to another user or organization:

```bash
curl -s -X POST "$HOST/repos/$OWNER/$REPO/transfer" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  -d '{"new_owner": "newowner"}'
```

Returns `202 Accepted`. The repository URL changes to reflect the new owner.
Old URLs redirect to the new location.
