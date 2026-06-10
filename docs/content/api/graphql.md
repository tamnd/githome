---
title: "GraphQL"
description: "query and mutate githome data through the GraphQL v4-compatible endpoint"
weight: 90
---

Githome exposes a GraphQL endpoint that mirrors the GitHub GraphQL API v4. Clients built for GitHub's GraphQL API work against githome without changes.

## Endpoint and authentication

```
POST /api/graphql
Authorization: Bearer <token>
Content-Type: application/json
```

Send queries and mutations as JSON with a `query` field and an optional `variables` map:

```bash
curl -X POST \
  -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:3000/api/graphql \
  -d '{"query": "{ viewer { login } }"}'
```

## Using with gh CLI

The `gh api graphql` command targets the configured host automatically:

```bash
gh api graphql -f query='{ viewer { login name email } }'
```

Pass variables with `-F`:

```bash
gh api graphql \
  -f query='query($owner: String!, $name: String!) {
    repository(owner: $owner, name: $name) { id stargazerCount }
  }' \
  -F owner=alice \
  -F name=myrepo
```

## Key queries

### viewer

Retrieve the authenticated user's profile and their repositories:

```graphql
query {
  viewer {
    login
    name
    email
    bio
    repositories(first: 10, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        name
        description
        stargazerCount
        isPrivate
        updatedAt
      }
    }
  }
}
```

### repository

Fetch a repository with its default branch, open issues, and open pull requests:

```graphql
query {
  repository(owner: "alice", name: "myrepo") {
    id
    name
    description
    stargazerCount
    forkCount
    isPrivate
    defaultBranchRef {
      name
      target {
        ... on Commit {
          oid
          message
          committedDate
          author { name email }
        }
      }
    }
    issues(first: 5, states: [OPEN], orderBy: {field: CREATED_AT, direction: DESC}) {
      totalCount
      nodes {
        number
        title
        state
        author { login }
        labels(first: 5) { nodes { name color } }
        createdAt
      }
    }
    pullRequests(first: 5, states: [OPEN]) {
      totalCount
      nodes {
        number
        title
        author { login }
        headRefName
        baseRefName
        isDraft
        createdAt
      }
    }
  }
}
```

### user

Public profile lookup by login:

```graphql
query {
  user(login: "alice") {
    id
    login
    name
    bio
    company
    location
    createdAt
    repositories(first: 5, privacy: PUBLIC) {
      totalCount
      nodes { name stargazerCount }
    }
  }
}
```

### node

Look up any object by its node ID. Use `... on TypeName` fragments to access type-specific fields:

```graphql
query {
  node(id: "R_abc123") {
    ... on Repository {
      name
      stargazerCount
    }
    ... on Issue {
      number
      title
      state
    }
    ... on PullRequest {
      number
      title
      merged
    }
  }
}
```

Node IDs come from the `id` field on any object. Pass them to `node()` for efficient direct lookups without traversing owner/repo paths.

### search

Search across repositories, issues, and users:

```graphql
query {
  search(query: "language:Go fork:false", type: REPOSITORY, first: 10) {
    repositoryCount
    pageInfo {
      hasNextPage
      endCursor
    }
    edges {
      node {
        ... on Repository {
          nameWithOwner
          description
          stargazerCount
          primaryLanguage { name }
        }
      }
    }
  }
}
```

Valid `type` values: `REPOSITORY`, `ISSUE`, `USER`.

For issue and PR search:

```graphql
query {
  search(query: "is:open label:bug repo:alice/myrepo", type: ISSUE, first: 20) {
    issueCount
    nodes {
      ... on Issue {
        number
        title
        labels(first: 5) { nodes { name } }
        author { login }
      }
      ... on PullRequest {
        number
        title
        isDraft
        author { login }
      }
    }
  }
}
```

## Relay pagination

All connection fields support the Relay cursor pagination pattern. Use `first` and `after` (or `last` and `before`) with the `pageInfo` object:

```graphql
query ListIssues($cursor: String) {
  repository(owner: "alice", name: "myrepo") {
    issues(first: 25, after: $cursor, states: [OPEN]) {
      pageInfo {
        hasNextPage
        endCursor
      }
      totalCount
      nodes {
        number
        title
        createdAt
      }
    }
  }
}
```

Walk all pages:

```bash
CURSOR=""
while true; do
  RESULT=$(gh api graphql \
    -f query='query($cursor: String) {
      repository(owner: "alice", name: "myrepo") {
        issues(first: 25, after: $cursor, states: [OPEN]) {
          pageInfo { hasNextPage endCursor }
          nodes { number title }
        }
      }
    }' \
    -F cursor="$CURSOR")

  echo "$RESULT" | jq '.data.repository.issues.nodes[].title'

  HAS_NEXT=$(echo "$RESULT" | jq -r '.data.repository.issues.pageInfo.hasNextPage')
  [ "$HAS_NEXT" = "true" ] || break
  CURSOR=$(echo "$RESULT" | jq -r '.data.repository.issues.pageInfo.endCursor')
done
```

## PR timeline and review threads

Fetch a pull request with its review threads:

```graphql
query {
  repository(owner: "alice", name: "myrepo") {
    pullRequest(number: 42) {
      id
      title
      state
      reviewDecision
      reviews(first: 10) {
        nodes {
          id
          author { login }
          state
          body
          submittedAt
        }
      }
      reviewThreads(first: 20) {
        nodes {
          id
          isResolved
          path
          line
          comments(first: 10) {
            nodes {
              id
              author { login }
              body
              createdAt
              diffHunk
              position
            }
          }
        }
      }
    }
  }
}
```

## Key mutations

### Issues

```graphql
# Create an issue
mutation {
  createIssue(input: {
    repositoryId: "R_abc123"
    title: "Panic on empty config"
    body: "Steps to reproduce..."
    labelIds: ["LA_xyz"]
    assigneeIds: ["U_bob"]
  }) {
    issue { number url }
  }
}

# Update an issue
mutation {
  updateIssue(input: {
    id: "I_abc123"
    title: "Updated title"
    body: "Updated body"
    state: CLOSED
  }) {
    issue { number state }
  }
}

# Close an issue
mutation {
  closeIssue(input: { issueId: "I_abc123" }) {
    issue { number state }
  }
}
```

### Pull requests

```graphql
# Create a pull request
mutation {
  createPullRequest(input: {
    repositoryId: "R_abc123"
    baseRefName: "main"
    headRefName: "feature/rate-limit"
    title: "Add rate limiting"
    body: "Implements token bucket rate limiting."
    draft: false
  }) {
    pullRequest { number url }
  }
}

# Update a pull request
mutation {
  updatePullRequest(input: {
    pullRequestId: "PR_abc123"
    title: "Add rate limiting (revised)"
    body: "Updated description."
  }) {
    pullRequest { number title }
  }
}

# Merge a pull request
mutation {
  mergePullRequest(input: {
    pullRequestId: "PR_abc123"
    mergeMethod: SQUASH
    commitHeadline: "Add rate limiting (#42)"
  }) {
    pullRequest { number merged mergedAt }
  }
}
```

Valid `mergeMethod` values: `MERGE`, `SQUASH`, `REBASE`.

### Reviews

```graphql
# Add a pending review with inline comments
mutation {
  addPullRequestReview(input: {
    pullRequestId: "PR_abc123"
    body: "Looks good overall, one nit below."
    event: COMMENT
    comments: [
      {
        path: "src/handler.go"
        position: 12
        body: "Consider returning early here."
      }
    ]
  }) {
    pullRequestReview { id state }
  }
}

# Submit a pending review
mutation {
  submitPullRequestReview(input: {
    pullRequestReviewId: "PRR_abc123"
    event: APPROVE
    body: "LGTM"
  }) {
    pullRequestReview { id state submittedAt }
  }
}

# Delete a pending review
mutation {
  deletePullRequestReview(input: {
    pullRequestReviewId: "PRR_abc123"
  }) {
    pullRequestReview { id }
  }
}

# Add an inline review comment
mutation {
  addPullRequestReviewComment(input: {
    pullRequestId: "PR_abc123"
    body: "This should be guarded."
    path: "src/handler.go"
    position: 8
  }) {
    comment { id url }
  }
}
```

### Labels

```graphql
# Create a label
mutation {
  createLabel(input: {
    repositoryId: "R_abc123"
    name: "performance"
    color: "0075ca"
    description: "Performance improvements"
  }) {
    label { id name }
  }
}

# Add labels to an issue or PR
mutation {
  addLabelsToLabelable(input: {
    labelableId: "I_abc123"
    labelIds: ["LA_xyz", "LA_pqr"]
  }) {
    labelable {
      ... on Issue { number labels(first: 5) { nodes { name } } }
    }
  }
}
```

### Review threads

```graphql
# Resolve a review thread
mutation {
  resolveReviewThread(input: { threadId: "PRRT_abc123" }) {
    thread { id isResolved }
  }
}

# Unresolve a review thread
mutation {
  unresolveReviewThread(input: { threadId: "PRRT_abc123" }) {
    thread { id isResolved }
  }
}
```

## Node IDs

Every object has an `id` field that is a stable, globally unique node ID. Two formats are in use:

- Legacy: `base64("TypeNameDB_ID")`, e.g. `base64("Repository10")` becomes `UmVwb3NpdG9yeTEw`
- New: `PREFIX_<base64url(payload)>`, e.g. `R_abc123` for a repository

Node ID prefixes:

| Prefix | Type |
|--------|------|
| `U_` | User |
| `O_` | Organization |
| `R_` | Repository |
| `I_` | Issue |
| `PR_` | PullRequest |
| `IC_` | IssueComment |
| `PRR_` | PullRequestReview |
| `PRRC_` | PullRequestReviewComment |
| `PRRT_` | PullRequestReviewThread |
| `CR_` | CheckRun |
| `CS_` | CheckSuite |
| `SC_` | StatusContext |
| `LA_` | Label |
| `MI_` | Milestone |
| `CO_` | Commit |
| `RE_` | Reaction |
| `RL_` | Release |
| `RLA_` | ReleaseAsset |
| `G_` | Gist |
| `GC_` | GistComment |

Both formats decode correctly via the `node()` query.

## Rate limiting

Query current rate limit state:

```graphql
query {
  rateLimit {
    limit
    remaining
    resetAt
    used
    nodeCount
  }
}
```

GraphQL mutations and expensive queries consume more rate limit than simple reads. The `nodeCount` field tracks how many nodes were loaded to produce the response, which contributes to rate limit cost.

## Introspection

Standard GraphQL introspection is fully supported:

```bash
# Fetch the full schema
gh api graphql -f query='{ __schema { types { name kind } } }'

# Inspect a specific type
gh api graphql -f query='
  {
    __type(name: "Repository") {
      name
      fields {
        name
        type { name kind ofType { name kind } }
        description
      }
    }
  }
'
```

Use introspection to discover available fields before writing queries, or to validate your schema assumptions.

## Error shape

GraphQL always returns HTTP 200 even for errors. Check the `errors` array in the response body:

```json
{
  "data": null,
  "errors": [
    {
      "message": "Could not resolve to a Repository with the name 'missing'.",
      "locations": [{ "line": 2, "column": 3 }],
      "path": ["repository"],
      "type": "NOT_FOUND"
    }
  ]
}
```

Partial success is possible: `data` may contain some resolved fields alongside errors for fields that failed. Always check both `data` and `errors`.

Authentication failures return HTTP 401 before the GraphQL layer is reached:

```json
{ "message": "Requires authentication", "documentation_url": "..." }
```
