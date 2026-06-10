---
title: "Configuration reference"
description: "every environment variable githome reads at startup"
weight: 10
---

## Minimal configs

**SQLite (default, simplest):**

```sh
GITHOME_DATABASE_URL=sqlite:///var/lib/githome/githome.sqlite
GITHOME_DATA_DIR=/var/lib/githome
GITHOME_HTML_BASE_URL=https://git.example.com
GITHOME_WEB_ENABLED=true
GITHOME_ENV=production
```

**PostgreSQL:**

```sh
GITHOME_DATABASE_URL=postgres://githome:secret@localhost/githome?sslmode=disable
GITHOME_DATA_DIR=/var/lib/githome
GITHOME_HTML_BASE_URL=https://git.example.com
GITHOME_WEB_ENABLED=true
GITHOME_ENV=production
```

## Full reference

| Variable | Default | Description |
|---|---|---|
| `GITHOME_DATABASE_URL` | `sqlite:///var/lib/githome/githome.sqlite` | Database. SQLite (`sqlite:///path`) or PostgreSQL (`postgres://...`). |
| `GITHOME_DATA_DIR` | `/var/lib/githome` | Root directory for git bare repos. |
| `GITHOME_HTML_BASE_URL` | `http://localhost:3000` | Public-facing URL. Used in OAuth redirects, git clone URLs, and webhook payloads. Must match what users type in a browser. |
| `GITHOME_LISTEN_HTTP` | `:3000` | Bind address. Use `unix:/tmp/githome.sock` for a Unix socket. |
| `GITHOME_ENV` | `development` | Set to `production` to enable strict cookies and disable debug pages. |
| `GITHOME_SESSION_KEY` | auto-generated | At least 32 bytes. Auto-generated on first start. Set explicitly to keep sessions valid across restarts. |
| `GITHOME_TOKEN_PEPPER` | auto-generated | At least 16 bytes. Changing this invalidates all existing tokens. |
| `GITHOME_WEB_ENABLED` | `true` | Enable the server-rendered web UI. |
| `GITHOME_WEB_SITE_NAME` | `githome` | Display name shown in the web UI. |
| `GITHOME_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. `debug` logs SQL queries and git operations. |
| `GITHOME_LOG_FORMAT` | `text` | `text` (human-readable) or `json` (for log aggregation with Loki, Datadog, etc.). |
| `GITHOME_DB_POOL_SIZE` | `10` | PostgreSQL connection pool size. Ignored for SQLite. |
| `GITHOME_SERVER_READ_TIMEOUT` | `15s` | HTTP read timeout. |
| `GITHOME_SERVER_WRITE_TIMEOUT` | `30s` | HTTP write timeout. |
| `GITHOME_SERVER_IDLE_TIMEOUT` | `120s` | HTTP keep-alive idle timeout. |
| `GITHOME_SERVER_MAX_BLOB_BYTES` | `10485760` | Maximum size for file blob API responses (10 MiB). Does not limit git push size. |
| `GITHOME_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout. |
| `GITHOME_GIT_BINARY_PATH` | from `$PATH` | Override the `git` binary used for git operations. |

## Notes

`SESSION_KEY` and `TOKEN_PEPPER` are auto-generated on first start and persist
in the data directory. They survive container restarts when using a volume
mount. If you manage secrets externally (HashiCorp Vault, AWS Secrets Manager),
inject them as environment variables before first start.

Changing `TOKEN_PEPPER` invalidates all existing personal access tokens. Users
will need to regenerate their tokens.
