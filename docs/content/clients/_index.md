---
title: "Clients"
description: "tools and libraries that work with githome out of the box"
weight: 5
---

githome implements the GitHub REST API v3 and GraphQL API v4 wire protocols.
Any client that works with GitHub works with githome by pointing it at your
host instead of `api.github.com`.

The pages in this section walk through the most common clients:

| Client | Guide |
|--------|-------|
| `gh` CLI | [GitHub CLI]({{< relref "github-cli" >}}) |
| Octokit (JS, Python, Go, Ruby) | [Octokit]({{< relref "octokit" >}}) |
| Terraform GitHub provider | [Terraform]({{< relref "terraform" >}}) |

## Authentication

All authenticated requests use a personal access token (PAT). Pass it as a
bearer token:

```http
Authorization: Bearer <token>
```

or the older GitHub form, which githome also accepts:

```http
Authorization: token <token>
```

Create a PAT from your account settings at `https://<your-host>/user/settings/tokens`.

## Base URL

The REST API lives at the root of your host. There is no `/api/v3` prefix.
Set the base URL to `https://git.example.com` (no trailing path component).

The GraphQL endpoint is `POST https://git.example.com/api/graphql`.

## Required request header

Set `Accept: application/vnd.github+json` on REST requests. Most client
libraries handle this automatically.

## Git transport

githome serves the Git Smart HTTP protocol. Clone with:

```sh
git clone http://localhost:3000/owner/repo.git
```

Authenticate by embedding a token in the URL or using a credential helper:

```sh
git clone http://user:TOKEN@localhost:3000/owner/repo.git
```

SSH transport is not yet implemented.
