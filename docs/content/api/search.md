---
title: "Search"
description: "search repositories, issues, pull requests, and code using the githome REST API"
weight: 75
---

Githome exposes three search endpoints that mirror the GitHub search API. All search endpoints accept a `q` parameter that combines keywords with typed qualifiers.

## Authentication

Search requests must include a valid token for non-public instances. Unauthenticated requests can only reach public resources.

```
Authorization: Bearer <token>
Accept: application/vnd.github+json
```

## Search repositories

```
GET /search/repositories
```

**Query parameters**

| Parameter | Description |
|-----------|-------------|
| `q` | Search query with qualifiers |
| `sort` | `stars`, `forks`, `updated`, `best-match` (default) |
| `order` | `asc` or `desc` (default `desc`) |
| `per_page` | Results per page, max 100 (default 30) |
| `page` | Page number (default 1) |

Find Go repositories that are not forks, sorted by stars:

```bash
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:3000/search/repositories?q=fork:false+language:Go&sort=stars&order=desc&per_page=20"
```

Response:

```json
{
  "total_count": 142,
  "incomplete_results": false,
  "items": [
    {
      "id": 12,
      "name": "myrepo",
      "full_name": "alice/myrepo",
      "private": false,
      "owner": { "login": "alice", "id": 3 },
      "description": "A Go project",
      "fork": false,
      "stargazers_count": 88,
      "forks_count": 14,
      "language": "Go",
      "updated_at": "2026-05-01T10:00:00Z"
    }
  ]
}
```

Qualifiers for repository search:

| Qualifier | Example | Meaning |
|-----------|---------|---------|
| `language:` | `language:Go` | Primary language |
| `fork:` | `fork:false` | Exclude or include forks |
| `user:` | `user:alice` | Owned by a specific user |
| `org:` | `org:myorg` | Owned by an organization |
| `repo:` | `repo:alice/myrepo` | Specific repository |
| `topic:` | `topic:cli` | Has a topic tag |
| `is:` | `is:public` | Visibility: `public` or `private` |
| `stars:` | `stars:>50` | Star count range |
| `size:` | `size:<1000` | Size in KB |
| `created:` | `created:>2025-01-01` | Creation date |
| `pushed:` | `pushed:>2026-01-01` | Last push date |

## Search issues and pull requests

```
GET /search/issues
```

Issues and pull requests share one endpoint. Use `type:issue` or `type:pr` to narrow results.

Find open bug issues in a specific repository:

```bash
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:3000/search/issues?q=is:open+label:bug+repo:alice/myrepo"
```

Find open pull requests assigned to a user:

```bash
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:3000/search/issues?q=is:open+type:pr+assignee:alice"
```

Response shape:

```json
{
  "total_count": 7,
  "incomplete_results": false,
  "items": [
    {
      "id": 55,
      "number": 12,
      "title": "nil pointer on empty input",
      "state": "open",
      "user": { "login": "bob" },
      "labels": [{ "name": "bug", "color": "d73a4a" }],
      "assignee": { "login": "alice" },
      "milestone": null,
      "created_at": "2026-04-10T08:00:00Z",
      "updated_at": "2026-04-11T09:00:00Z",
      "pull_request": null
    }
  ]
}
```

When an item is a pull request, the `pull_request` field is present:

```json
"pull_request": {
  "url": "http://localhost:3000/repos/alice/myrepo/pulls/5",
  "html_url": "http://localhost:3000/alice/myrepo/pull/5",
  "merged_at": null
}
```

Issue and PR qualifiers:

| Qualifier | Example | Meaning |
|-----------|---------|---------|
| `is:` | `is:open`, `is:closed`, `is:merged` | State |
| `type:` | `type:issue`, `type:pr` | Kind |
| `repo:` | `repo:alice/myrepo` | Scope to repository |
| `label:` | `label:bug` | Has label (repeatable) |
| `assignee:` | `assignee:alice` | Assigned to user |
| `author:` | `author:bob` | Created by user |
| `involves:` | `involves:alice` | Author, assignee, commenter, or mentioned |
| `commenter:` | `commenter:alice` | Has a comment from user |
| `milestone:` | `milestone:v2` | Belongs to milestone |
| `no:` | `no:assignee`, `no:label`, `no:milestone` | Missing field |
| `reactions:` | `reactions:>5` | Reaction count |
| `interactions:` | `interactions:>10` | Total interactions |
| `created:` | `created:>2026-01-01` | Creation date |
| `updated:` | `updated:<2026-06-01` | Last update date |
| `closed:` | `closed:>2025-12-01` | Close date |

## Search code

```
GET /search/code
```

Code search scans file contents across repositories. The `repo:` qualifier is required to scope the search; full cross-repository code search is not supported in this release.

Find all files containing the word `function` in the `src/` path of a repository:

```bash
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:3000/search/code?q=function+repo:alice/myrepo+path:src/"
```

Find usages of a specific function name:

```bash
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:3000/search/code?q=ParseConfig+repo:alice/myrepo+language:Go"
```

Response:

```json
{
  "total_count": 3,
  "incomplete_results": false,
  "items": [
    {
      "name": "config.go",
      "path": "src/config.go",
      "sha": "a1b2c3d4",
      "url": "http://localhost:3000/repos/alice/myrepo/contents/src/config.go",
      "repository": {
        "id": 12,
        "full_name": "alice/myrepo"
      },
      "text_matches": [
        {
          "fragment": "func ParseConfig(path string) (*Config, error) {",
          "matches": [{ "text": "ParseConfig", "indices": [5, 16] }]
        }
      ]
    }
  ]
}
```

Code search qualifiers:

| Qualifier | Example | Meaning |
|-----------|---------|---------|
| `repo:` | `repo:alice/myrepo` | Scope to repository (required) |
| `path:` | `path:src/` | File path prefix or substring |
| `language:` | `language:Go` | File language |
| `filename:` | `filename:config.go` | Exact filename |
| `extension:` | `extension:go` | File extension |

## Sorting

Not all sort fields apply to every endpoint. The table below shows which fields are valid per endpoint.

| Sort value | Repositories | Issues/PRs | Code |
|------------|:---:|:---:|:---:|
| `best-match` | yes | yes | yes |
| `stars` | yes | no | no |
| `forks` | yes | no | no |
| `updated` | yes | yes | no |
| `created` | no | yes | no |
| `reactions` | no | yes | no |
| `interactions` | no | yes | no |

## Pagination

Use `per_page` (max 100) and `page` to walk through results. The response does not include a `Link` header in this release; compute offsets from `total_count`.

```bash
# Page 2, 50 results per page
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:3000/search/repositories?q=language:Go&per_page=50&page=2"
```

`incomplete_results: true` means the query timed out before scanning all data. The `items` array still contains partial results.

## Rate limits

Search endpoints apply a stricter rate limit than other REST endpoints: 30 requests per minute for authenticated users. Exceeding this returns HTTP 429 with a `Retry-After` header indicating seconds to wait.

```bash
# Check remaining search quota via rate limit endpoint
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:3000/rate_limit"
```

```json
{
  "resources": {
    "search": {
      "limit": 30,
      "remaining": 28,
      "reset": 1749600000
    }
  }
}
```
