---
title: "Webhooks"
description: "create and manage repository webhooks and verify delivery signatures"
weight: 80
---

Webhooks send HTTP POST requests to a URL you control whenever events occur in a repository. Githome mirrors the GitHub webhooks API including delivery history, redelivery, and HMAC-SHA256 signature verification.

## Create a webhook

```
POST /repos/{owner}/{repo}/hooks
```

```bash
curl -X POST \
  -H "Authorization: Bearer TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  http://localhost:3000/repos/alice/myrepo/hooks \
  -d '{
    "config": {
      "url": "https://hooks.example.com/githome",
      "content_type": "json",
      "secret": "s3cr3t",
      "insecure_ssl": "0"
    },
    "events": ["push", "pull_request", "issues"],
    "active": true
  }'
```

Response (201 Created):

```json
{
  "id": 7,
  "name": "web",
  "active": true,
  "events": ["push", "pull_request", "issues"],
  "config": {
    "url": "https://hooks.example.com/githome",
    "content_type": "json",
    "insecure_ssl": "0"
  },
  "created_at": "2026-06-10T08:00:00Z",
  "updated_at": "2026-06-10T08:00:00Z"
}
```

The `secret` field is write-only; it never appears in GET responses. Set `insecure_ssl` to `"1"` only for development; githome will skip TLS certificate validation when delivering to that hook.

## Supported events

| Event name | Triggered when |
|------------|---------------|
| `push` | Commits pushed to any branch |
| `pull_request` | PR opened, closed, merged, edited, synchronized, labeled, assigned, review requested |
| `pull_request_review` | Review submitted, dismissed, or edited |
| `pull_request_review_comment` | Inline review comment created, edited, or deleted |
| `issues` | Issue opened, closed, edited, labeled, assigned, milestoned |
| `issue_comment` | Comment created, edited, or deleted on an issue or PR |
| `create` | Branch or tag created |
| `delete` | Branch or tag deleted |
| `release` | Release published, created, edited, or deleted |
| `ping` | Sent when a webhook is first created or when you call the ping endpoint |

Use `"*"` in the `events` array to subscribe to all events.

## List, get, and update webhooks

List all webhooks on a repository:

```bash
curl -H "Authorization: Bearer TOKEN" \
  http://localhost:3000/repos/alice/myrepo/hooks
```

Get a single webhook:

```bash
curl -H "Authorization: Bearer TOKEN" \
  http://localhost:3000/repos/alice/myrepo/hooks/7
```

Update events or config:

```bash
curl -X PATCH \
  -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:3000/repos/alice/myrepo/hooks/7 \
  -d '{
    "events": ["push", "pull_request", "issues", "release"],
    "active": true,
    "config": {
      "url": "https://hooks.example.com/githome-v2"
    }
  }'
```

Only fields present in the request body are updated. Omitting `config` leaves the existing config unchanged.

## Delete a webhook

```bash
curl -X DELETE \
  -H "Authorization: Bearer TOKEN" \
  http://localhost:3000/repos/alice/myrepo/hooks/7
```

Returns 204 No Content on success.

## Ping a webhook

Send a test ping delivery without triggering a real event:

```bash
curl -X POST \
  -H "Authorization: Bearer TOKEN" \
  http://localhost:3000/repos/alice/myrepo/hooks/7/pings
```

Returns 204. The delivery appears in the delivery history with event `ping`.

## Delivery history

List recent deliveries for a webhook:

```bash
curl -H "Authorization: Bearer TOKEN" \
  http://localhost:3000/repos/alice/myrepo/hooks/7/deliveries
```

```json
[
  {
    "id": 301,
    "guid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    "delivered_at": "2026-06-10T09:15:00Z",
    "redelivery": false,
    "duration": 0.231,
    "status": "OK",
    "status_code": 200,
    "event": "push",
    "action": null,
    "url": "https://hooks.example.com/githome"
  }
]
```

Get the full request and response for one delivery:

```bash
curl -H "Authorization: Bearer TOKEN" \
  http://localhost:3000/repos/alice/myrepo/hooks/deliveries/301
```

The response includes `request.headers`, `request.payload`, `response.headers`, and `response.payload` fields with the raw content.

## Redeliver a delivery

```bash
curl -X POST \
  -H "Authorization: Bearer TOKEN" \
  http://localhost:3000/repos/alice/myrepo/hooks/deliveries/301/attempts
```

Returns 202 Accepted. The redelivery appears as a new entry in the delivery list with `redelivery: true`.

## Signature verification

Every delivery carries these headers:

| Header | Value |
|--------|-------|
| `X-GitHub-Event` | Event name, e.g. `push` |
| `X-GitHub-Delivery` | UUID identifying this delivery |
| `X-GitHub-Hook-ID` | Numeric webhook ID |
| `X-Hub-Signature-256` | `sha256=<hex digest>` |

The signature is HMAC-SHA256 of the raw request body using the hook secret as the key. Always verify before processing the payload.

### Python example

```python
import hashlib
import hmac

def verify_signature(secret: str, body: bytes, signature_header: str) -> bool:
    if not signature_header.startswith("sha256="):
        return False
    expected = hmac.new(
        secret.encode(),
        msg=body,
        digestmod=hashlib.sha256,
    ).hexdigest()
    received = signature_header[len("sha256="):]
    return hmac.compare_digest(expected, received)

# In your request handler:
# body = request.get_data()  # raw bytes, before JSON parsing
# ok = verify_signature("s3cr3t", body, request.headers["X-Hub-Signature-256"])
```

### Go example

```go
package main

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "net/http"
    "strings"
)

func verifySignature(secret string, body []byte, sigHeader string) error {
    if !strings.HasPrefix(sigHeader, "sha256=") {
        return errors.New("missing sha256= prefix")
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := hex.EncodeToString(mac.Sum(nil))
    received := sigHeader[len("sha256="):]
    if !hmac.Equal([]byte(expected), []byte(received)) {
        return errors.New("signature mismatch")
    }
    return nil
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    if err := verifySignature("s3cr3t", body, r.Header.Get("X-Hub-Signature-256")); err != nil {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }
    // safe to parse body now
}
```

Always read the full raw body before parsing JSON. Parsers may normalize whitespace, changing the byte sequence and breaking signature verification.

## Payload examples

### push payload

```json
{
  "ref": "refs/heads/main",
  "before": "abc123",
  "after": "def456",
  "repository": {
    "id": 12,
    "name": "myrepo",
    "full_name": "alice/myrepo",
    "private": false,
    "owner": { "login": "alice" }
  },
  "pusher": { "name": "alice", "email": "alice@example.com" },
  "commits": [
    {
      "id": "def456",
      "message": "fix: handle nil pointer",
      "timestamp": "2026-06-10T08:00:00Z",
      "url": "http://localhost:3000/alice/myrepo/commit/def456",
      "author": { "name": "Alice", "email": "alice@example.com", "username": "alice" },
      "added": ["src/handler.go"],
      "removed": [],
      "modified": ["src/config.go"]
    }
  ],
  "head_commit": { "id": "def456", "message": "fix: handle nil pointer" }
}
```

### pull_request payload

```json
{
  "action": "opened",
  "number": 42,
  "pull_request": {
    "id": 200,
    "number": 42,
    "state": "open",
    "title": "Add rate limiting",
    "body": "Implements token bucket rate limiting per user.",
    "user": { "login": "bob" },
    "head": {
      "ref": "feature/rate-limit",
      "sha": "aaa111",
      "repo": { "full_name": "bob/myrepo" }
    },
    "base": {
      "ref": "main",
      "sha": "bbb222",
      "repo": { "full_name": "alice/myrepo" }
    },
    "merged": false,
    "draft": false,
    "created_at": "2026-06-10T09:00:00Z",
    "updated_at": "2026-06-10T09:00:00Z"
  },
  "repository": {
    "id": 12,
    "full_name": "alice/myrepo"
  },
  "sender": { "login": "bob" }
}
```

The `action` field describes what happened. For `pull_request`, possible values are `opened`, `closed`, `edited`, `synchronize`, `labeled`, `unlabeled`, `assigned`, `unassigned`, `review_requested`, `review_request_removed`, `converted_to_draft`, and `ready_for_review`.
