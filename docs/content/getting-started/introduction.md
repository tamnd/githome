---
title: "Why githome"
description: "the case for running your own GitHub-compatible forge"
weight: 10
---

## The problem with hosted forges

GitHub is excellent. It is also a service you do not control: rate limits,
policy changes, outages, and pricing all happen on someone else's schedule.
Alternatives like GitLab are more self-hostable but they are large, complex
systems with significant operational overhead.

githome takes a different approach: implement GitHub's API contract faithfully,
stay small enough to run on a single server, and ship as a single binary with no
external services required.

## Who runs githome

**Individual developers** who want their side projects on their own hardware,
accessible from a home lab, without paying for a hosted plan or managing a full
GitLab instance.

**Small teams** that have a private VPS or cloud VM and want proper code review,
issues, and webhooks without a SaaS subscription. Five engineers on a $10/month
VPS instead of $240/year per seat.

**Air-gapped environments** such as regulated industries (finance, defense,
healthcare) where code must never leave a controlled network. githome has no
outbound network requirements and runs without internet access.

**Cost-conscious engineering teams** where per-seat pricing becomes significant
at scale, or where a budget audit is coming.

## What you keep

Because githome speaks the GitHub API, you keep your entire toolchain. The URL
paths, JSON shapes, and auth headers are identical to GitHub. Swap the host,
keep everything else:

- `gh` CLI: `gh auth login --hostname your-githome-host`
- VS Code and JetBrains git integrations: no change needed
- Octokit (JS, Python, Go, Ruby): set `baseUrl` once
- Terraform GitHub provider: set `base_url` in the provider block
- CI runners: GitHub Actions support is in progress in tamnd/githome-action (not yet served by the githome binary)

See [Connect your tools](/getting-started/connect-your-tools/) for the exact
config change for each tool.

## What githome does not do

githome focuses on the code collaboration core. It does not implement GitHub
Packages, Copilot, Dependabot, Projects, or Pages. If you need those, you need
GitHub or a heavier alternative.

See the [compatibility reference](/reference/github-compatibility/) for the full
list of what is and is not supported.
