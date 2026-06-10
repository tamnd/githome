---
title: "GitHub CLI"
description: "using the gh cli with a self-hosted githome instance"
weight: 10
---

The `gh` CLI is the primary command-line interface for githome. Every `gh`
subcommand that targets GitHub works against githome by supplying your host
with `--hostname` or by setting `GH_HOST`.

## Installation

```sh
# macOS
brew install gh

# Linux (Debian/Ubuntu)
sudo apt install gh

# or download a binary from
# https://cli.github.com
```

Verify the install:

```sh
gh --version
```

## Authentication

### Interactive

```sh
gh auth login --hostname git.example.com
```

`gh` prompts for the protocol and a token. Select **HTTPS** and paste a PAT
when asked.

### Non-interactive

```sh
echo "$GITHOME_TOKEN" | gh auth login --hostname git.example.com --with-token
```

Useful in CI or scripts where interactive prompts are not available.

### Verify

```sh
gh auth status --hostname git.example.com
```

Expected output:

```
git.example.com
  Logged in to git.example.com account alice (keyring)
  Token: ghp_...
```

## Default host

Set the default host so you can omit `--hostname` on every command:

```sh
gh config set -h git.example.com git_protocol http
export GH_HOST=git.example.com
```

The `GH_HOST` environment variable takes precedence over the config file
value, which is convenient for shell sessions or CI pipelines.

The config file is at `~/.config/gh/hosts.yml`. A typical entry looks like:

```yaml
git.example.com:
    oauth_token: ghp_...
    git_protocol: http
    user: alice
```

## Repository operations

```sh
# List repos for the authenticated user
gh repo list

# List repos for another user or org
gh repo list myorg

# Create a repo
gh repo create myorg/myrepo --public --description "My project"

# Clone a repo
gh repo clone myorg/myrepo

# View repo details (opens in terminal; add --web to open browser)
gh repo view myorg/myrepo

# Rename and change visibility
gh repo edit myorg/myrepo --repo-name new-name --visibility private

# Add topics
gh repo edit myorg/myrepo --add-topic go --add-topic api

# Fork
gh repo fork upstream/project --clone

# Delete (requires confirmation or --yes)
gh repo delete myorg/myrepo --yes
```

## Issue operations

```sh
# List open issues
gh issue list --repo myorg/myrepo

# Filter by label or state
gh issue list --repo myorg/myrepo --label bug --state open

# Create an issue
gh issue create --repo myorg/myrepo \
  --title "Unexpected nil panic in handler" \
  --body "Steps to reproduce: ..."

# View issue 42
gh issue view 42 --repo myorg/myrepo

# Edit title and labels
gh issue edit 42 --repo myorg/myrepo --title "New title" --add-label enhancement

# Close and reopen
gh issue close 42 --repo myorg/myrepo
gh issue reopen 42 --repo myorg/myrepo

# Add a comment
gh issue comment 42 --repo myorg/myrepo --body "Fixed in commit abc123."
```

## Pull request operations

```sh
# List open PRs
gh pr list --repo myorg/myrepo

# Create a PR from the current branch
gh pr create --title "feat: add rate limiter" --body "Closes #42"

# View PR 7
gh pr view 7 --repo myorg/myrepo

# Check out a PR locally
gh pr checkout 7 --repo myorg/myrepo

# Show diff
gh pr diff 7 --repo myorg/myrepo

# Review (approve, request-changes, or comment)
gh pr review 7 --approve
gh pr review 7 --request-changes --body "Please add tests."
gh pr review 7 --comment --body "Looks good overall."

# Merge (merge commit, squash, or rebase)
gh pr merge 7 --squash --delete-branch

# Close and reopen
gh pr close 7 --repo myorg/myrepo
gh pr reopen 7 --repo myorg/myrepo

# Show all PRs that involve the current user
gh pr status
```

## Release operations

```sh
# List releases
gh release list --repo myorg/myrepo

# Create a release from a tag
gh release create v1.0.0 --title "v1.0.0" --notes "First stable release."

# Attach build artifacts
gh release upload v1.0.0 dist/myapp-linux-amd64 dist/myapp-darwin-amd64

# Download release assets
gh release download v1.0.0 --repo myorg/myrepo --dir /tmp/release

# Delete a release
gh release delete v1.0.0 --repo myorg/myrepo --yes
```

## Gist operations

```sh
# List gists for the authenticated user
gh gist list

# Create a gist from a file
gh gist create main.go --desc "HTTP middleware example"

# Create from stdin
echo "SELECT 1" | gh gist create --filename query.sql

# View a gist
gh gist view <gist-id>

# Edit a gist
gh gist edit <gist-id>

# Delete a gist
gh gist delete <gist-id>
```

## Raw API calls

Call any REST endpoint directly when `gh` does not have a dedicated subcommand:

```sh
# GET request
gh api /repos/myorg/myrepo --hostname git.example.com

# POST with JSON body
gh api /repos/myorg/myrepo/issues \
  --method POST \
  -f title="Bug report" \
  -f body="Details here."

# Paginate through results
gh api /repos/myorg/myrepo/issues --paginate
```

GraphQL queries work the same way:

```sh
gh api graphql --hostname git.example.com -f query='
  query {
    repository(owner: "myorg", name: "myrepo") {
      id
      name
      stargazerCount
    }
  }
'
```

## Environment variables

| Variable | Purpose |
|----------|---------|
| `GH_HOST` | Default hostname, overrides config file |
| `GH_TOKEN` | Token for the active host, overrides stored credential |
| `GH_REPO` | Default `owner/repo` for repo-scoped commands |
| `GH_DEBUG` | Set to `1` to print HTTP request/response details |
