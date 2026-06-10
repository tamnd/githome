---
title: "GitHub compatibility"
description: "which GitHub API features githome implements and which clients work against it without modification"
weight: 10
---

githome targets full wire compatibility with the GitHub REST API v3 and GraphQL v4. Any client written for GitHub should work against githome without code changes. This page documents what is supported, what is not, and which clients have been verified.

## Philosophy

githome implements the same HTTP contract as GitHub: same paths, same JSON shapes, same status codes, same auth headers. The goal is that you can point `gh`, Octokit, Terraform, or your IDE at a different base URL and everything works. There is no "compatibility mode" toggle. The server always speaks the GitHub wire protocol.

Two response headers help clients orient themselves:

```
X-GitHub-Enterprise-Version: githome
X-GitHub-Enterprise-Host: git.example.com
```

The header `X-GitHub-Api-Version: 2022-11-28` is accepted on requests but not required.

## Tested clients

The following clients have been verified against githome and are expected to continue working:

| Client | Notes |
|--------|-------|
| `gh` CLI (all versions) | Full command set: repo, issue, pr, gist, release, auth, api |
| Octokit.js (`@octokit/rest`) | REST and GraphQL plugins |
| Octokit.py (`PyGithub`) | Full API surface |
| Octokit.rb (`octokit` gem) | Full API surface |
| `google/go-github` | REST client |
| `terraform-provider-github` | Resource management via REST |
| VS Code Git | Clone, push, pull, branch operations |
| JetBrains IDEs | Built-in Git integration |

To configure `gh` against a githome instance:

```bash
gh auth login --hostname git.example.com --with-token <<< "$GITHOME_TOKEN"
gh repo list --hostname git.example.com
```

## Feature matrix

| Feature | Supported | Notes |
|---------|-----------|-------|
| Repositories (CRUD) | Yes | Create, read, update, delete, fork, topics |
| Branches | Yes | List, get, protect via refs API |
| Tags | Yes | List, annotated and lightweight |
| Commits | Yes | List, get, compare via REST and git objects API |
| Git contents API | Yes | File read, create, update, delete; blob and tree endpoints |
| Git Smart HTTP | Yes | Clone, fetch, push over HTTP with token auth; SSH planned |
| Issues | Yes | Labels, milestones, assignees, reactions, comments |
| PR lifecycle | Yes | Create, update, merge (merge/squash/rebase), files, commits |
| Code review | Yes | Inline comments, review submit (APPROVE/REQUEST_CHANGES/COMMENT), dismiss |
| Review requests | Yes | Request and remove reviewers |
| Check runs | Yes | Create, update, list; used by githome-action |
| Check suites | Yes | Auto-created on push |
| Commit statuses | Yes | Create and list legacy status contexts |
| Releases | Yes | Create, update, delete, asset upload and download |
| Gists | Yes | Create, update, delete, fork, star, comments |
| Webhooks | Yes | Per-repo hooks with HMAC-SHA256 signatures; redeliver |
| Search (repos/issues/code) | Yes | Full-text search via `/search/repositories`, `/search/issues`, `/search/code` |
| OAuth web flow | Yes | `POST /login/oauth/access_token` |
| OAuth device flow | Yes | Device code exchange |
| GitHub Apps tokens | Yes | `POST /app/installations/{id}/access_tokens` |
| PATs (personal access tokens) | Yes | Bearer and `token` schemes |
| Events timeline | Yes | `/events`, `/repos/{owner}/{repo}/events`, `/users/{username}/events` |
| Organizations and teams | Yes | CRUD for orgs, members, teams |
| Node IDs (legacy + new format) | Yes | Both formats accepted; new format emitted; see [Node IDs](../node-ids/) |
| GraphQL v4 | Yes | Core types: Repository, User, Issue, PullRequest, Ref, Commit, Release, Gist, and more |
| SAML SSO | No | Not planned |
| Packages / container registry | No | Not planned; use a separate registry |
| GitHub Copilot API | No | Not applicable to self-hosted |
| Dependabot | No | Not planned |
| Code scanning / SARIF | No | Not planned |
| Organization billing API | No | Not applicable |
| GitHub Pages | No | Not planned |
| Wiki | No | Not planned |
| Projects (v2 board) | No | Not planned |
| Notifications | No | Not planned |
| Starring / watching | No | Not planned |
| Actions runner engine | Via `tamnd/githome-action` | Separate binary that implements the runner registration and job dispatch protocol |

## Actions runners

githome does not bundle a workflow engine. The companion project `tamnd/githome-action` implements the GitHub Actions runner protocol as a standalone binary. Once installed, workflow files in `.github/workflows/` run against your githome instance the same way they run on GitHub.

```bash
# Register a runner against a githome instance
githome-action register \
  --url http://git.example.com \
  --token RUNNER_REGISTRATION_TOKEN \
  --name my-runner
```

See the [githome-action repository](https://github.com/tamnd/githome-action) for setup details.

## API versioning

githome does not enforce an API version. The `X-GitHub-Api-Version` request header is accepted and ignored. This matches GitHub's behavior for clients that omit the header, because breaking changes would break existing clients.

```bash
# Both of these work identically
curl -H "Authorization: Bearer $TOKEN" http://localhost:3000/user
curl -H "Authorization: Bearer $TOKEN" \
     -H "X-GitHub-Api-Version: 2022-11-28" \
     http://localhost:3000/user
```

## Known differences

- **Rate limiting.** githome currently returns stub `X-RateLimit-*` headers (limit=5000, remaining=4999). Real throttling is not enforced.
- **Pagination.** Link headers use the same rel=next/prev/first/last format as GitHub, but `per_page` is capped at 100.
- **Emoji reactions.** The full set of GitHub reaction types is supported: `+1`, `-1`, `laugh`, `confused`, `heart`, `hooray`, `rocket`, `eyes`.
- **Markdown rendering.** githome renders GFM via the `POST /markdown` endpoint. The output is functionally equivalent but not byte-identical to GitHub's renderer.
