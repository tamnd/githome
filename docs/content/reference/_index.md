---
title: "Reference"
description: "complete technical reference for githome's API, configuration, and compatibility"
weight: 5
---

This section is the authoritative reference for every knob, endpoint, and behavior in githome. It assumes you already have a running instance. If you are still getting started, see the [Getting started](../getting-started/) section first.

## Pages in this section

**[GitHub compatibility](github-compatibility/)** -- Which GitHub API features githome implements, which are not yet supported, and which third-party clients have been tested against it. Includes the full compatibility matrix.

**[Node IDs](node-ids/)** -- How githome assigns opaque identifiers to GraphQL `id` fields and REST `node_id` fields. Covers the legacy base64 format, the newer prefix format, all resource kinds, and how to use node IDs in both GraphQL and REST.

**[Errors](errors/)** -- The shape of every error response githome returns, the meaning of each HTTP status code, how to read the `errors` array on 422 responses, and how to interpret GraphQL error payloads.

**[Environment variables](environment-variables/)** -- Every `GITHOME_*` variable with its type, default, and a description. Grouped by subsystem. Includes minimal example configs for SQLite and PostgreSQL deployments.

---
