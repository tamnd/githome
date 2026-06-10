---
title: "Gists"
description: "create, retrieve, update, and manage named code snippets via the githome gist API"
weight: 60
---

## What gists are

A gist is a named collection of one or more files, each with its own filename and content. Gists can be `public` (visible to unauthenticated users and indexed on `GET /gists/public`) or secret (accessible only by direct URL or to authenticated owners). Each gist has its own git repository, so you can clone it, push revisions, and inspect history.

Gist IDs are 20-character lowercase hex strings, for example `a3f9c12b4d8e7f0123ab`. This matches the GitHub format.

## Authentication

Public gists are readable without authentication. Creating, starring, forking, and all write operations require a bearer token.

```bash
export TOKEN=ghp_...
```

## Create a gist

`POST /gists`

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/gists \
  -d '{
    "description": "HTTP client with retry",
    "public": true,
    "files": {
      "client.go": {
        "content": "package main\n\nimport \"net/http\"\n\nfunc main() {}\n"
      },
      "README.md": {
        "content": "# retry client\nA simple HTTP client with backoff.\n"
      }
    }
  }'
```

### Gist object

```json
{
  "id": "a3f9c12b4d8e7f0123ab",
  "node_id": "G_kgDOB...",
  "description": "HTTP client with retry",
  "public": true,
  "owner": {
    "login": "alice",
    "id": 1,
    "node_id": "U_kgDOA...",
    "avatar_url": "https://git.example.com/avatars/u/1",
    "type": "User"
  },
  "user": null,
  "truncated": false,
  "files": {
    "client.go": {
      "filename": "client.go",
      "type": "application/x-go",
      "language": "Go",
      "raw_url": "https://git.example.com/gists/a3f9c12b4d8e7f0123ab/raw/client.go",
      "size": 55,
      "truncated": false,
      "content": "package main\n\nimport \"net/http\"\n\nfunc main() {}\n"
    },
    "README.md": {
      "filename": "README.md",
      "type": "text/markdown",
      "language": "Markdown",
      "raw_url": "https://git.example.com/gists/a3f9c12b4d8e7f0123ab/raw/README.md",
      "size": 44,
      "truncated": false,
      "content": "# retry client\nA simple HTTP client with backoff.\n"
    }
  },
  "forks": [],
  "history": [],
  "forks_url": "https://git.example.com/gists/a3f9c12b4d8e7f0123ab/forks",
  "commits_url": "https://git.example.com/gists/a3f9c12b4d8e7f0123ab/commits",
  "git_pull_url": "https://git.example.com/a3f9c12b4d8e7f0123ab.git",
  "git_push_url": "https://git.example.com/a3f9c12b4d8e7f0123ab.git",
  "html_url": "https://git.example.com/alice/a3f9c12b4d8e7f0123ab",
  "url": "https://git.example.com/gists/a3f9c12b4d8e7f0123ab",
  "created_at": "2026-06-10T08:00:00Z",
  "updated_at": "2026-06-10T08:00:00Z"
}
```

## List gists

**Authenticated user's gists:**

`GET /gists`

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "https://git.example.com/gists?per_page=30"
```

**Public gists (no auth needed):**

`GET /gists/public`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  "https://git.example.com/gists/public?per_page=30"
```

**Starred gists:**

`GET /gists/starred`

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/starred
```

**Another user's public gists:**

`GET /users/{username}/gists`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/users/alice/gists
```

All list endpoints return an array of gist objects. The `files` map in list responses omits the `content` field; fetch the individual gist to get file content.

## Get a gist

`GET /gists/{id}`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab
```

## Update a gist

`PATCH /gists/{id}`

You can rename files, add files, change content, or delete files in a single request.

- To add or update a file: include the filename with a `content` key.
- To rename a file: include the old filename in the map, set `filename` to the new name.
- To delete a file: set the file's value to `null`.

```bash
curl -s -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab \
  -d '{
    "description": "HTTP client with retry and timeout",
    "files": {
      "client.go": {
        "content": "package main\n\nimport (\n\t\"net/http\"\n\t\"time\"\n)\n\nfunc main() { _ = &http.Client{Timeout: 5 * time.Second} }\n"
      },
      "notes.txt": {
        "content": "Bump timeout to 5s per review feedback."
      },
      "README.md": null
    }
  }'
```

In the example above: `client.go` is updated, `notes.txt` is added, and `README.md` is deleted.

## Delete a gist

`DELETE /gists/{id}`

Returns `204 No Content`.

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab
```

## Star and unstar

**Star:**

`PUT /gists/{id}/star`

Returns `204 No Content`.

```bash
curl -s -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/star
```

**Unstar:**

`DELETE /gists/{id}/star`

Returns `204 No Content`.

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/star
```

**Check if starred:**

`GET /gists/{id}/star`

Returns `204 No Content` if starred, `404 Not Found` if not.

```bash
curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/star
# 204 or 404
```

## Fork a gist

`POST /gists/{id}/forks`

Creates a copy of the gist under the authenticated user's account. Returns the new gist object.

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/forks
```

**List forks:**

`GET /gists/{id}/forks`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/forks
```

## Comments

Comments attach to a gist, not to individual files.

### List comments

`GET /gists/{id}/comments`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/comments
```

### Create a comment

`POST /gists/{id}/comments`

```bash
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/comments \
  -d '{"body": "Tested on Go 1.23. Works perfectly."}'
```

### Get a comment

`GET /gists/{id}/comments/{comment_id}`

```bash
curl -s \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/comments/12
```

### Update a comment

`PATCH /gists/{id}/comments/{comment_id}`

```bash
curl -s -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/comments/12 \
  -d '{"body": "Tested on Go 1.23 and 1.24. Works perfectly."}'
```

### Delete a comment

`DELETE /gists/{id}/comments/{comment_id}`

Returns `204 No Content`.

```bash
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://git.example.com/gists/a3f9c12b4d8e7f0123ab/comments/12
```
