---
title: "SSH Keys"
description: "managing SSH public keys through the REST API and future SSH transport plans"
weight: 20
---

SSH git transport is not yet implemented in githome. The planned feature will
support the standard `git@HOST:owner/repo.git` syntax. Track progress in the
issue tracker.

Even without SSH transport, githome stores SSH public keys per user and exposes
them through the REST API. This is useful for deploy-key workflows with other
hosts, for tooling that provisions access by reading keys from the API, and for
compatibility with clients that enumerate `GET /users/{username}/keys`.

## Add an SSH key

```bash
curl -X POST http://localhost:3000/user/keys \
  -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "laptop ed25519",
    "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... alice@laptop"
  }'
```

Response:

```json
{
  "id": 7,
  "title": "laptop ed25519",
  "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... alice@laptop",
  "verified": false,
  "created_at": "2026-06-10T09:00:00Z",
  "read_only": false,
  "url": "http://localhost:3000/user/keys/7"
}
```

The `key` field must be a complete authorized-keys line including the key type
and optional comment. RSA, ECDSA, and Ed25519 keys are accepted.

## List SSH keys for the authenticated user

```bash
curl http://localhost:3000/user/keys \
  -H "Authorization: Bearer TOKEN"
```

Response is an array of key objects:

```json
[
  {
    "id": 7,
    "title": "laptop ed25519",
    "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... alice@laptop",
    "verified": false,
    "created_at": "2026-06-10T09:00:00Z",
    "read_only": false,
    "url": "http://localhost:3000/user/keys/7"
  }
]
```

## List public SSH keys for any user

This endpoint is unauthenticated:

```bash
curl http://localhost:3000/users/alice/keys
```

Returns the same array shape, but only the `id`, `key`, and `url` fields are
present for public access.

## Delete an SSH key

```bash
curl -X DELETE http://localhost:3000/user/keys/7 \
  -H "Authorization: Bearer TOKEN"
```

Returns `204 No Content` on success. Deleting a key that belongs to another
user returns `404`.

## Key fingerprint

The API response does not include the fingerprint directly. Compute it locally:

```bash
echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI..." | ssh-keygen -lf -
```

Output:

```
256 SHA256:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789ABCD alice@laptop (ED25519)
```

## Deploy keys

Deploy keys are SSH keys scoped to a single repository. They are not tied to a
user account. A deploy key can be read-only or read-write. This is the correct
way to grant CI systems or deployment scripts access to one repository without
creating a machine user.

### Add a deploy key

```bash
curl -X POST http://localhost:3000/repos/alice/myrepo/keys \
  -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "ci-server",
    "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... ci@buildbox",
    "read_only": true
  }'
```

Response:

```json
{
  "id": 42,
  "title": "ci-server",
  "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... ci@buildbox",
  "verified": false,
  "created_at": "2026-06-10T09:05:00Z",
  "read_only": true,
  "url": "http://localhost:3000/repos/alice/myrepo/keys/42"
}
```

Set `"read_only": false` to allow pushes from the deploy key.

### List deploy keys

```bash
curl http://localhost:3000/repos/alice/myrepo/keys \
  -H "Authorization: Bearer TOKEN"
```

### Delete a deploy key

```bash
curl -X DELETE http://localhost:3000/repos/alice/myrepo/keys/42 \
  -H "Authorization: Bearer TOKEN"
```

Returns `204 No Content`.

## Future SSH transport

When SSH transport ships, the clone URL will be:

```
git@HOST:owner/repo.git
```

or with an explicit user:

```
ssh://git@HOST/owner/repo.git
```

Keys already stored through the API will be used for authentication
automatically; no migration will be needed.
