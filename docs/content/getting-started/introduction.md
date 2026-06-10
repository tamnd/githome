---
title: "Introduction"
description: "what githome is, who it is for, and how GitHub compatibility works"
weight: 10
---

githome is a self-hosted forge that reimplements the GitHub REST API v3, GraphQL v4, and git Smart HTTP transport inside a single Go binary. You point `gh`, `octokit`, Terraform, or your IDE's git integration at your own server and they work without modification.

## Who it is for

githome is built for teams that need code hosting under their own control:

- **Private infrastructure.** You run the binary on your own hardware or cloud VMs. No data leaves your network.
- **Air-gapped environments.** githome has no external dependencies at runtime. SQLite mode requires no separate database process. A single binary plus a data directory is a complete installation.
- **Full ownership.** The database is a standard SQLite file or a Postgres schema. You can back it up, migrate it, or inspect it with ordinary tools. No proprietary storage formats.
- **GitHub Actions on self-hosted runners.** The companion project `tamnd/githome-action` implements the Actions runner protocol so your existing workflow files run against githome.

## Architecture

githome runs as a single OS process. Inside that process:

```
                  ┌────────────────────────────────────┐
  gh / curl /     │              githome                │
  Octokit / git ──►  HTTP listener (:3000 by default)  │
                  │                                     │
                  │  REST API v3    /repos /issues ...  │
                  │  GraphQL v4     /api/graphql        │
                  │  Git Smart HTTP /{owner}/{repo}.git │
                  │  Web UI         / (optional)        │
                  │                                     │
                  │  SQLite (default) or Postgres       │
                  │  Git bare repos on local filesystem │
                  └────────────────────────────────────┘
```

All four layers share one HTTP listener and one database connection pool. There is no sidecar, no message queue, and no external service dependency unless you choose Postgres.

The web UI is optional. Set `GITHOME_WEB_ENABLED=false` to run in pure API mode.

## What works today

### Repositories and git

Full repository lifecycle: create, update, delete, fork. Git Smart HTTP for clone, fetch, and push over HTTP with token authentication. Branch and tag management via both the REST refs API and direct git operations.

```bash
git clone http://alice:TOKEN@git.example.com/alice/myrepo.git
git push origin main
```

### Issues and pull requests

Issues with labels, milestones, assignees, reactions, and comments. Pull requests with full code review: multi-file inline comments, review submission with APPROVE/REQUEST_CHANGES/COMMENT, and review dismissal. The file diff and commit list APIs are implemented so review tools that call those endpoints work correctly.

### Code review workflow

```bash
# gh CLI works against githome out of the box
gh pr create --base main --head feature/foo --title "Add foo"
gh pr review 42 --approve
gh pr merge 42 --squash
```

### Releases and assets

Create releases tied to a tag, upload binary assets, and serve them back. The `/releases/latest` and `/releases/tags/{tag}` shortcuts are implemented, matching GitHub's behavior exactly.

### Gists

Full gist support: create, update, delete, fork, star, and comment. Public gist listing is available without authentication. Gists are backed by bare git repositories, so `git clone` works on each one.

### Webhooks

Repository webhooks with configurable events, secret HMAC signing, delivery history, redeliver, and ping. The payload shapes match GitHub's so existing webhook consumers work without changes.

### OAuth and tokens

Personal access tokens (PATs) are the primary authentication mechanism. githome also implements the GitHub OAuth web flow and device flow so applications that use GitHub OAuth can be pointed at a githome instance by changing one base URL. GitHub Apps installation tokens are supported via `POST /app/installations/{id}/access_tokens`.

### Search

Repository, issue, and code search with the same query syntax as GitHub's search API.

## How GitHub compatibility works

githome achieves compatibility at three levels:

**URL paths.** Every endpoint is at the same path as on GitHub. A client configured with `https://api.github.com` as base URL needs only that string changed to point at githome.

**JSON shapes.** Response bodies use the same field names and types as GitHub's documented API responses. Fields githome does not yet implement are omitted rather than set to wrong values, which is what GitHub does when features are disabled.

**Auth headers.** Both `Authorization: Bearer <token>` and `Authorization: token <token>` are accepted, matching GitHub's dual-format support.

Clients set the standard GitHub content type header:

```
Accept: application/vnd.github+json
```

Example: the `gh` CLI needs only `GH_HOST` set:

```bash
export GH_HOST=git.example.com
gh repo list
gh issue list --repo alice/myrepo
```

Example: Octokit.js needs only `baseUrl`:

```js
import { Octokit } from "@octokit/rest";

const octokit = new Octokit({
  auth: "YOUR_TOKEN",
  baseUrl: "https://git.example.com",
});

const { data: repo } = await octokit.repos.get({
  owner: "alice",
  repo: "myrepo",
});
```

Example: the Terraform GitHub provider needs `base_url`:

```hcl
provider "github" {
  token    = var.githome_token
  base_url = "https://git.example.com/"
}
```

## Next steps

Follow the [Quick start](../quick-start/) to run githome locally with Docker Compose and make your first API call in five minutes.
