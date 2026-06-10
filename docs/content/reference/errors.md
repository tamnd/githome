---
title: "Errors"
description: "http status codes, error response shapes, and how to interpret githome error payloads"
weight: 30
---

githome returns errors in the same format as the GitHub REST API. Every error response is a JSON object with a `message` field. Validation errors include an `errors` array with per-field detail. A `documentation_url` field may point to relevant docs.

## REST error shape

```json
{
  "message": "Validation Failed",
  "errors": [
    {
      "resource": "Repository",
      "field": "name",
      "code": "already_exists"
    }
  ],
  "documentation_url": "https://docs.github.com/rest"
}
```

Fields:

- `message`: human-readable summary of the error.
- `errors`: present on 422 responses; each entry describes one validation failure.
- `documentation_url`: optional; included when a related docs page exists.

## HTTP status codes

### 400 Bad Request

The request body could not be parsed or a required field is structurally wrong (for example, malformed JSON).

```bash
curl -s -X POST http://localhost:3000/user/repos \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d 'not json' | jq .
```

```json
{
  "message": "Problems parsing JSON"
}
```

### 401 Unauthorized

No token was provided, or the token is invalid or expired.

```bash
curl -s http://localhost:3000/user | jq .
```

```json
{
  "message": "requires authentication",
  "documentation_url": "https://docs.github.com/rest"
}
```

Always send the `Authorization` header:

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:3000/user
# or the legacy form accepted by older Octokit versions:
curl -H "Authorization: token $TOKEN" http://localhost:3000/user
```

### 403 Forbidden

The caller is authenticated but lacks the required permission. Common cases: pushing to a repository where you have only read access, calling an admin endpoint without admin rights, or trying to delete another user's resource.

```json
{
  "message": "Must have admin rights to Repository."
}
```

Private repositories also return 403 (not 404) when the caller is authenticated but not a member, to distinguish "exists but forbidden" from "does not exist".

### 404 Not Found

The resource does not exist, or the caller cannot see it. githome returns 404 for private repositories when the caller is not authenticated, to avoid leaking the existence of private repos.

```bash
curl -s http://localhost:3000/repos/alice/secret-repo | jq .
```

```json
{
  "message": "Not Found"
}
```

### 409 Conflict

A conflict prevents the operation from completing. The most common case is creating a branch that already exists.

```bash
curl -s -X POST http://localhost:3000/repos/alice/myrepo/git/refs \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ref":"refs/heads/main","sha":"abc123"}' | jq .
```

```json
{
  "message": "Reference already exists"
}
```

### 422 Unprocessable Entity

The request is structurally valid but fails validation rules. The `errors` array contains one entry per failed field.

```bash
curl -s -X POST http://localhost:3000/user/repos \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"my repo with spaces"}' | jq .
```

```json
{
  "message": "Validation Failed",
  "errors": [
    {
      "resource": "Repository",
      "field": "name",
      "code": "invalid"
    }
  ]
}
```

**Error codes in `errors[].code`:**

| Code | Meaning |
|------|---------|
| `missing` | A required resource does not exist (for example, a referenced label) |
| `missing_field` | A required field was not provided |
| `already_exists` | The value conflicts with an existing record |
| `invalid` | The value is syntactically or semantically wrong |
| `custom` | A situation-specific message; read `errors[].message` for detail |

When `code` is `custom`, the entry also includes a `message` field with a human-readable explanation:

```json
{
  "resource": "PullRequest",
  "field": "base",
  "code": "custom",
  "message": "base branch must exist in the repository"
}
```

### 429 Too Many Requests

The caller has exceeded the rate limit. Check the response headers:

```
X-RateLimit-Limit:     5000
X-RateLimit-Remaining: 0
X-RateLimit-Reset:     1718000000
Retry-After:           60
```

`X-RateLimit-Reset` is a Unix timestamp. `Retry-After` is seconds to wait before retrying.

```json
{
  "message": "API rate limit exceeded for user ID 7.",
  "documentation_url": "https://docs.github.com/rest/overview/rate-limits-for-the-rest-api"
}
```

### 500 Internal Server Error

An unexpected server-side error occurred. The response body contains a generic message; the detail is in the server logs.

```json
{
  "message": "internal server error"
}
```

Check the githome process logs (`GITHOME_LOG_LEVEL=debug` gives more detail) and open an issue if the problem is reproducible.

## GraphQL errors

GraphQL always returns HTTP 200. Errors appear in the `errors` array of the response body alongside a partial (or null) `data` field.

```bash
curl -s -X POST http://localhost:3000/api/graphql \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query":"{ repository(owner: \"x\", name: \"y\") { name } }"}' | jq .
```

```json
{
  "data": {
    "repository": null
  },
  "errors": [
    {
      "message": "Could not resolve to a Repository with the name 'x/y'.",
      "locations": [{ "line": 1, "column": 3 }],
      "path": ["repository"]
    }
  ]
}
```

Fields:

- `message`: human-readable description.
- `locations`: line and column in the query where the error was detected.
- `path`: the field path in the response where the error occurred.

Authentication errors in GraphQL return HTTP 401, not HTTP 200, because the request cannot be processed at all.

## Testing error handling

Trigger each type deliberately to verify your error handling code:

```bash
# 401: no token
curl -s http://localhost:3000/user

# 403: wrong permissions (requires a repo you do not admin)
curl -s -X DELETE -H "Authorization: Bearer $READ_ONLY_TOKEN" \
  http://localhost:3000/repos/alice/myrepo

# 404: nonexistent resource
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:3000/repos/nobody/nothing

# 422: validation error
curl -s -X POST http://localhost:3000/user/repos \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":""}'

# 409: duplicate ref
curl -s -X POST http://localhost:3000/repos/alice/myrepo/git/refs \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"ref\":\"refs/heads/main\",\"sha\":\"$(git rev-parse HEAD)\"}"
```
