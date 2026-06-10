---
title: "Git Transport"
description: "how githome serves git repositories over HTTP and manages SSH keys"
weight: 5
---

Githome serves git repositories using the Git Smart HTTP protocol. Any git client
that supports Smart HTTP works without modification: the standard `git` CLI,
VS Code, JetBrains IDEs, and the `gh` CLI all connect transparently.

## Protocol support

| Protocol | Status |
|----------|--------|
| Smart HTTP (`http://`, `https://`) | Supported |
| SSH (`git@host:owner/repo.git`) | Planned |

Clone and push URLs follow the form:

```
http://HOST/{owner}/{repo}.git
https://HOST/{owner}/{repo}.git
```

See [Smart HTTP]({{< relref "smart-http" >}}) for the full setup guide including
authentication, credential helpers, and TLS termination.

## SSH key management

Even though SSH git transport is not yet supported, githome stores and exposes
SSH public keys through the REST API. This is useful for deploy-key workflows
with other hosts and for tooling that reads keys from the API. See
[SSH Keys]({{< relref "ssh-keys" >}}) for the API reference.
