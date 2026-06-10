---
title: "GitHub compatibility"
description: "what works with githome and what does not"
weight: 10
---

githome targets full wire compatibility with the GitHub REST API v3 and GraphQL
v4. Clients written for GitHub work against githome without modification. Only
the host changes.

## Tested clients

| Client | How to connect |
|---|---|
| gh CLI | `gh auth login --hostname your-host` |
| Octokit.js | `new Octokit({ baseUrl: "https://your-host" })` |
| PyGitHub | `Github(base_url="https://your-host", login_or_token="TOKEN")` |
| go-github | `.WithEnterpriseURLs("https://your-host/", "https://your-host/")` |
| Octokit.rb | `Octokit::Client.new(api_endpoint: "https://your-host/")` |
| Terraform github provider | `base_url = "https://your-host/"` |
| VS Code (git) | No change needed |
| JetBrains (git) | No change needed |
| GitHub Actions runners | Use tamnd/githome-action |

## Supported features

| Feature | Status |
|---|---|
| Repositories (CRUD, topics, visibility) | Supported |
| Branches, tags, commits | Supported |
| File contents API | Supported |
| Issues (CRUD, labels, assignees, milestones, reactions) | Supported |
| Pull requests (create, merge, update) | Supported |
| Code review (reviews, inline comments, resolve threads) | Supported |
| Webhooks (events, HMAC-SHA256 signatures, delivery logs) | Supported |
| Releases and release assets | Supported |
| Gists (CRUD, stars, forks, comments) | Supported |
| Search (repositories, issues, code) | Supported |
| OAuth web flow | Supported |
| OAuth device flow | Supported |
| GitHub App installation tokens | Supported |
| Personal access tokens | Supported |
| GraphQL v4 (queries, mutations, relay pagination) | Supported |
| Git Smart HTTP (clone, fetch, push) | Supported |
| Check runs and status contexts | Supported |
| GitHub Actions (CI workflows) | Supported via tamnd/githome-action |

## Not supported

| Feature | Notes |
|---|---|
| SSH transport | Planned |
| GitHub Packages / Container Registry | Not planned |
| GitHub Copilot | Not applicable |
| Dependabot | Not planned |
| GitHub Pages (hosting) | Not planned |
| Projects v2 | Not planned |
| Code scanning / SARIF | Not planned |
| SAML SSO | Not planned |
| Merge queue | Not planned |
| Notifications (email / web) | Not planned |
