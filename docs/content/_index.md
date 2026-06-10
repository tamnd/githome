---
title: "githome"
description: "your own GitHub, no subscription, no vendor lock-in"
---

githome is a GitHub-compatible forge you run yourself. It speaks the same REST
API and GraphQL that GitHub does, so every tool that works with GitHub works
with githome without modification: gh CLI, Octokit, Terraform, VS Code,
JetBrains, and any CI runner.

You get private repositories, issues, pull requests, code review, webhooks,
releases, and gists. All in a single binary with no external dependencies.

## Why run your own

**Full ownership.** Your code, your data, your rules. No terms of service
changes, no account suspensions, no rate limits you did not set yourself.

**Air-gap friendly.** githome runs anywhere a Go binary runs: a laptop, a
Raspberry Pi, an isolated datacenter network. No outbound connections required.

**GitHub-compatible.** You do not learn a new workflow. The URL paths, JSON
shapes, and auth headers are identical to GitHub. Swap the host and keep
everything else.

**Cost.** A VPS running githome costs a few dollars a month. GitHub Teams costs
$4/user/month. At 20 developers that is $960/year versus a $6/month VPS.

## Get started in one command

```sh
docker run -p 3000:3000 -v githome-data:/var/lib/githome \
  -e GITHOME_HTML_BASE_URL=http://localhost:3000 \
  -e GITHOME_WEB_ENABLED=true \
  ghcr.io/tamnd/githome:latest
```

Open http://localhost:3000, register, and start pushing code.
Follow the [quick-start guide](/getting-started/quick-start/) to connect your
existing tools.
