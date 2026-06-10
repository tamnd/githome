---
title: "Octokit"
description: "using octokit client libraries with githome in javascript, python, go, and ruby"
weight: 20
---

Octokit libraries wrap the GitHub REST API. Point them at your githome host
by overriding the base URL and they work without further modification.

## JavaScript / Node.js

### Install

```sh
npm install @octokit/rest
```

### Setup

```js
const { Octokit } = require("@octokit/rest");

const octokit = new Octokit({
  auth: "TOKEN",
  baseUrl: "http://localhost:3000",
});
```

### List repos

```js
const { data: repos } = await octokit.rest.repos.listForAuthenticatedUser({
  per_page: 30,
});
repos.forEach((r) => console.log(r.full_name));
```

### Create an issue

```js
const { data: issue } = await octokit.rest.issues.create({
  owner: "myorg",
  repo: "myrepo",
  title: "Something is broken",
  body: "Steps to reproduce...",
  labels: ["bug"],
});
console.log(`Created issue #${issue.number}`);
```

### List pull requests

```js
const { data: prs } = await octokit.rest.pulls.list({
  owner: "myorg",
  repo: "myrepo",
  state: "open",
});
prs.forEach((pr) => console.log(`#${pr.number} ${pr.title}`));
```

### Merge a pull request

```js
await octokit.rest.pulls.merge({
  owner: "myorg",
  repo: "myrepo",
  pull_number: 7,
  merge_method: "squash",
});
```

### Create a release

```js
const { data: release } = await octokit.rest.repos.createRelease({
  owner: "myorg",
  repo: "myrepo",
  tag_name: "v1.0.0",
  name: "v1.0.0",
  body: "First stable release.",
  draft: false,
  prerelease: false,
});
console.log(`Release URL: ${release.html_url}`);
```

## Python

### Install

```sh
pip install PyGithub
```

### Setup

```python
from github import Github

g = Github(
    base_url="http://localhost:3000",
    login_or_token="TOKEN",
)
```

### List repos

```python
user = g.get_user()
for repo in user.get_repos():
    print(repo.full_name)
```

### Create an issue

```python
repo = g.get_repo("myorg/myrepo")
issue = repo.create_issue(
    title="Something is broken",
    body="Steps to reproduce...",
    labels=["bug"],
)
print(f"Created issue #{issue.number}")
```

### List pull requests

```python
repo = g.get_repo("myorg/myrepo")
for pr in repo.get_pulls(state="open"):
    print(f"#{pr.number} {pr.title}")
```

## Go

### Install

```sh
go get github.com/google/go-github/v67/github
go get golang.org/x/oauth2
```

### Setup

```go
package main

import (
    "context"
    "fmt"

    "github.com/google/go-github/v67/github"
    "golang.org/x/oauth2"
)

func main() {
    ctx := context.Background()

    ts := oauth2.StaticTokenSource(
        &oauth2.Token{AccessToken: "TOKEN"},
    )
    httpClient := oauth2.NewClient(ctx, ts)

    client, err := github.NewClient(httpClient).
        WithAuthToken("TOKEN").
        WithEnterpriseURLs(
            "http://localhost:3000/",
            "http://localhost:3000/",
        )
    if err != nil {
        panic(err)
    }
    // ...
}
```

### List repos

```go
opts := &github.RepositoryListByAuthenticatedUserOptions{
    ListOptions: github.ListOptions{PerPage: 30},
}
repos, _, err := client.Repos.ListByAuthenticatedUser(ctx, opts)
if err != nil {
    panic(err)
}
for _, r := range repos {
    fmt.Println(r.GetFullName())
}
```

### Create an issue

```go
input := &github.IssueRequest{
    Title: github.Ptr("Something is broken"),
    Body:  github.Ptr("Steps to reproduce..."),
}
issue, _, err := client.Issues.Create(ctx, "myorg", "myrepo", input)
if err != nil {
    panic(err)
}
fmt.Printf("Created issue #%d\n", issue.GetNumber())
```

## Ruby

### Install

```sh
gem install octokit
```

### Setup

```ruby
require "octokit"

client = Octokit::Client.new(
  access_token: "TOKEN",
  api_endpoint: "http://localhost:3000/",
)
```

Note the trailing slash on `api_endpoint`. The Octokit Ruby library requires
it to construct endpoint URLs correctly.

### List repos

```ruby
client.repos.each do |repo|
  puts repo.full_name
end
```

### Create an issue

```ruby
issue = client.create_issue(
  "myorg/myrepo",
  "Something is broken",
  "Steps to reproduce...",
  labels: "bug",
)
puts "Created issue ##{issue.number}"
```

### List pull requests

```ruby
client.pull_requests("myorg/myrepo", state: "open").each do |pr|
  puts "##{pr.number} #{pr.title}"
end
```
