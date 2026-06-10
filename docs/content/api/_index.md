---
title: "API"
description: "githome exposes a GitHub REST API v3 and GraphQL v4 compatible surface"
weight: 5
---

githome exposes a GitHub REST API v3 compatible surface. Every endpoint accepts
the same request shape and returns the same JSON response shape as GitHub, so
any client library or tool built against the GitHub API works against a githome
server without modification.

Supported clients include the `gh` CLI, Octokit (JS/Python/Go/Ruby), git Smart
HTTP transport, GitHub Actions runners via `tamnd/githome-action`, the
Terraform GitHub provider, VS Code Git integration, and JetBrains IDEs.

## Base URL

All REST endpoints mount at the root of your githome instance:

```
https://git.example.com
```

The GraphQL endpoint is at `/api/graphql`.

## Request headers

| Header | Value |
|--------|-------|
| `Accept` | `application/vnd.github+json` (or `application/json`) |
| `Authorization` | `Bearer <token>` or `token <token>` |
| `X-GitHub-Api-Version` | `2022-11-28` (optional, good practice) |

## Sections

- [Authentication](authentication/) - PATs, OAuth web flow, device flow, GitHub App tokens, rate limiting
- [Repositories](repositories/) - create, read, update, delete, list, topics

## Endpoint groups

| Group | Description |
|-------|-------------|
| Auth / User | Current user profile, SSH keys, org memberships |
| Tokens / OAuth | Token exchange and revocation |
| Repositories | Repo CRUD, topics, visibility |
| Branches and Refs | Branch listing, git refs, tags |
| Contents | File and directory contents, blobs, trees |
| Issues | Issues, comments, labels, milestones, reactions |
| Pull Requests | PRs, inline comments, merge |
| Code Review | Reviews, review events, dismissals |
| Gists | Gist CRUD, starring, forking, comments |
| Releases | Release CRUD, assets, upload |
| Webhooks | Hook CRUD, deliveries, redelivery |
| Search | Repositories, issues, code |
| Events | Public timeline, repo events, user events |
| Teams / Orgs | Org info, members, teams |
| GitHub Apps | Installation access tokens |

## GraphQL

Send a `POST` request to `/api/graphql` with a JSON body:

```json
{
  "query": "query { viewer { login } }"
}
```

The schema mirrors the GitHub GraphQL v4 schema. Key types include
`Repository`, `User`, `Issue`, `PullRequest`, `PullRequestReview`, `Ref`,
`Commit`, `CheckRun`, `Label`, `Milestone`, `Release`, and `Gist`.

## Node IDs

githome uses the same node ID encoding as GitHub. Legacy IDs are
`base64("TypeNameDB_ID")`. New-format IDs use a type prefix followed by a
base64url-encoded payload, for example `R_` for repositories and `U_` for
users. The `node(id: "...")` GraphQL query resolves any node ID.
