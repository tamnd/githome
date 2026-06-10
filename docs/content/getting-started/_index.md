---
title: "Getting started"
description: "install, configure, and start using githome in minutes"
weight: 5
---

githome is a self-hosted GitHub-compatible forge: a single Go binary that speaks the GitHub REST API v3, GraphQL v4, and git Smart HTTP. You bring your own server; githome handles the rest.

This section walks you from zero to a running instance with real git pushes and API calls. The pages are ordered so each one builds on the previous.

## Pages in this section

**[Introduction](introduction/)** -- What githome is, who it is for, how GitHub compatibility works, and what features are available today. Start here if you are evaluating githome or explaining it to your team.

**[Quick start](quick-start/)** -- A working instance in five minutes using Docker Compose. Covers environment variables, creating a token, cloning a repository, pushing commits, and verifying everything through the REST API. The fastest path to a running server.

**[Installation](installation/)** -- All the ways to install the githome binary: pre-built releases for Linux, macOS, and Windows; `go install`; Docker; Homebrew; and distro packages (deb, rpm, apk). Includes minimum system requirements and how to verify the installation.

---

After this section, see **Configuration** for a full reference of every environment variable, and **API reference** for the complete endpoint list with request and response shapes.
