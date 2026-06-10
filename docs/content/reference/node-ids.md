---
title: "Node IDs"
description: "how githome assigns and formats opaque identifiers for GraphQL and REST responses"
weight: 20
---

Every resource in githome has a node ID. It appears as the `id` field in GraphQL responses and the `node_id` field in REST responses. Node IDs are opaque: do not parse their internal structure. Use them as handles to look up objects via `node(id: "...")` or to cross-reference between REST and GraphQL.

## Two formats

githome supports two formats. Both are accepted as input in all GraphQL queries and REST requests. The new prefix format is always emitted in responses.

### Legacy format

The legacy format is `base64(TypeName + DB_ID)` with no separator.

```
base64("Repository10") = "UmVwb3NpdG9yeTEw"
base64("User3")        = "VXNlcjM="
```

This matches the original GitHub format and is accepted for backward compatibility with clients that may have stored node IDs from an earlier version.

### New prefix format

The new format uses a short resource-type prefix followed by an underscore and a base64url-encoded payload.

```
R_kgDOABcxNg    # a Repository
U_kgDOAA4xyz    # a User
PR_kwDOABcx     # a PullRequest
```

The prefix unambiguously identifies the resource kind without decoding the payload. Clients that need to branch on resource type can read the prefix directly.

## Resource kinds and prefixes

| Prefix | Resource type |
|--------|---------------|
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

## Reading node IDs from REST

Every resource response includes `node_id`:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:3000/repos/alice/myrepo | jq '.node_id, .id'
```

```
"R_kgDOABcxNg"
10
```

The integer `id` is a database primary key. The `node_id` is the stable identifier to use in GraphQL and when storing references to resources.

## Using node IDs in GraphQL

The `node` root query accepts any node ID and returns the corresponding object. Use an inline fragment to access type-specific fields:

```graphql
query {
  node(id: "R_kgDOABcxNg") {
    ... on Repository {
      name
      owner { login }
      defaultBranchRef { name }
    }
  }
}
```

```bash
curl -s -X POST http://localhost:3000/api/graphql \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query":"{ node(id: \"R_kgDOABcxNg\") { ... on Repository { name } } }"}'
```

The `nodes` query accepts a list and returns objects in the same order, with `null` for IDs that do not exist or are not visible to the caller:

```graphql
query {
  nodes(ids: ["R_kgDOABcxNg", "I_kgDOABab"]) {
    ... on Repository { name }
    ... on Issue { title number }
  }
}
```

## Stability

Node IDs are stable for the lifetime of the resource. Renaming a repository or user does not change its node ID. Deleting a resource permanently retires its node ID; it is never reused.

Clients should store node IDs rather than constructing paths like `/repos/alice/myrepo` when they need a durable reference. The path changes on rename; the node ID does not.

## Legacy ID acceptance

If a client passes a legacy base64 node ID as input, githome decodes it and resolves the resource by its database ID. The response always returns the new prefix format. This means tools that stored legacy IDs continue to work without migration.

```graphql
# Legacy ID still resolves
query {
  node(id: "UmVwb3NpdG9yeTEw") {
    ... on Repository { name }
  }
}
```
