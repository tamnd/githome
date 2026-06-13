---
title: "Connect your tools"
description: "point gh CLI, VS Code, Octokit, Terraform, and CI runners at githome"
weight: 40
---

githome speaks the GitHub API, so connecting a tool is usually one config
change: swap the host.

## gh CLI

```sh
echo YOUR_TOKEN | gh auth login --hostname your-githome-host --with-token
gh config set default_hostname your-githome-host
```

Every `gh` command then works as-is: `gh repo list`, `gh pr create`,
`gh issue view`, and so on.

## git over HTTP

```sh
# Credentials in the URL (automation-friendly)
git clone http://alice:TOKEN@your-githome-host/alice/myrepo.git

# Credential helper (interactive-friendly)
git config --global credential.helper store
echo "machine your-githome-host login alice password TOKEN" >> ~/.netrc
```

## VS Code

VS Code's built-in git integration works without any change because it uses
the `git` binary directly. For PR and issue sidebars (GitHub Pull Requests
and Issues extension), authenticate via the extension's sign-in flow and enter
`https://your-githome-host` when prompted for the server.

## JetBrains IDEs

Settings > Version Control > GitHub, click **+**, choose **Log In with Token**.
Set the server to `https://your-githome-host` and paste your token.

## Octokit (JavaScript)

```js
const { Octokit } = require("@octokit/rest")

const octokit = new Octokit({
  auth: "YOUR_TOKEN",
  baseUrl: "https://your-githome-host",
})
```

## PyGitHub (Python)

```python
from github import Github

g = Github(
    base_url="https://your-githome-host",
    login_or_token="YOUR_TOKEN",
)
```

## go-github (Go)

```go
client, err := github.NewClient(nil).
    WithAuthToken("YOUR_TOKEN").
    WithEnterpriseURLs(
        "https://your-githome-host/",
        "https://your-githome-host/",
    )
```

## Terraform GitHub provider

```hcl
provider "github" {
  token    = var.githome_token
  base_url = "https://your-githome-host/"  # trailing slash required
  owner    = "myorg"
}
```

All standard `github_*` resources and data sources work against githome.

## GitHub Actions runners

GitHub Actions support is in progress in the separate
[tamnd/githome-action](https://github.com/tamnd/githome-action) project, which
reimplements the Actions runner and service protocol. The githome binary does
not serve the Actions API yet, so workflows do not run against a githome host
today. Follow that repository for status.
