---
title: "Environment variables"
description: "complete reference for all GITHOME_* environment variables, grouped by subsystem"
weight: 40
---

All githome configuration is done through environment variables with the `GITHOME_` prefix. Variables can also be set in a config file; see [Config file](#config-file) below.

## Core

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_ENV` | `production` | Runtime mode. `production` enables secure defaults (HTTPS-only cookies, stricter headers). `development` relaxes those for local work. |
| `GITHOME_HTML_BASE_URL` | *(required)* | Public-facing base URL, e.g. `https://git.example.com`. Used in clone URLs, webhook payloads, and email links. No trailing slash. |
| `GITHOME_SHUTDOWN_TIMEOUT` | `10s` | How long to wait for in-flight requests to finish when the process receives SIGTERM. |

## Database

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_DATABASE_URL` | `sqlite:///var/lib/githome/githome.sqlite` | Database connection string. Prefix `sqlite:///` for SQLite (absolute path) or a standard `postgres://user:pass@host/db` DSN for PostgreSQL. |
| `GITHOME_DB_POOL_SIZE` | `10` | Maximum number of open database connections in the pool. Increase for high-concurrency Postgres deployments. |

## Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_DATA_DIR` | `/var/lib/githome` | Directory where git bare repositories are stored. Must be readable and writable by the githome process. Each repository is stored as `{owner}/{repo}.git` under this path. |

## Server

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_LISTEN_HTTP` | `:3000` | Address and port for the HTTP listener. Use `0.0.0.0:3000` to bind all interfaces or `127.0.0.1:3000` to bind localhost only. |
| `GITHOME_SERVER_READ_TIMEOUT` | `15s` | Maximum time to read an entire request, including body. |
| `GITHOME_SERVER_WRITE_TIMEOUT` | `30s` | Maximum time to write a response. For large blob downloads, increase this value. |
| `GITHOME_SERVER_IDLE_TIMEOUT` | `120s` | Maximum time to wait for the next request on a keep-alive connection. |
| `GITHOME_SERVER_READ_HEADER_TIMEOUT` | `5s` | Maximum time to read request headers. Helps mitigate slow-header attacks. |
| `GITHOME_SERVER_MAX_BLOB_BYTES` | `10485760` | Maximum size in bytes for a single blob download via the contents API. Default is 10 MiB. Requests for larger blobs are rejected with 403. |

## Security

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_SESSION_KEY` | *(auto-generated)* | Secret used to sign HTTP session cookies. Must be at least 32 bytes, hex-encoded or raw. If not set, githome generates a random key on startup, which invalidates all sessions on restart. Set a stable value in production. |
| `GITHOME_TOKEN_PEPPER` | *(auto-generated)* | Additional secret mixed into PAT hashes before storage. Must be at least 16 bytes. If not set, githome generates a value on startup. Changing this value invalidates all existing tokens. Set a stable value in production. |
| `GITHOME_GIT_BINARY_PATH` | *(from PATH)* | Absolute path to the `git` binary. Defaults to the first `git` found in `PATH`. Set this when running in a restricted environment where PATH is not reliable. |

## Logging

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_LOG_LEVEL` | `info` | Minimum log level to emit. One of `debug`, `info`, `warn`, `error`. Use `debug` when diagnosing issues; it logs every request and SQL query. |
| `GITHOME_LOG_FORMAT` | `text` | Log output format. `text` is human-readable with color (when stdout is a terminal). `json` emits structured JSON lines, suitable for log aggregators like Loki, Datadog, or CloudWatch. |

## Web UI

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_WEB_ENABLED` | `true` | Set to `false` to disable the server-rendered web UI entirely. The API and git transport still work. Useful when githome is accessed only programmatically. |
| `GITHOME_WEB_SITE_NAME` | `Githome` | Display name shown in the web UI header, page titles, and email subjects. |

## Markup

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHOME_MARKUP_CAMO_SECRET` | *(disabled)* | HMAC secret for the Camo image proxy. When set alongside `GITHOME_MARKUP_CAMO_BASE_URL`, external images in Markdown are rewritten to go through the proxy. This prevents clients from leaking their IP to external image servers. |
| `GITHOME_MARKUP_CAMO_BASE_URL` | *(disabled)* | Base URL of the Camo proxy, e.g. `https://camo.example.com`. Required when `GITHOME_MARKUP_CAMO_SECRET` is set. |

## Config file

Instead of setting environment variables in the shell, you can put them in a flat `KEY=VALUE` file and point githome at it:

```bash
GITHOME_CONFIG_FILE=/etc/githome/githome.env githome serve
```

Inside the file, the `GITHOME_` prefix is optional. Both forms are equivalent:

```ini
# /etc/githome/githome.env

DATABASE_URL=postgres://githome:secret@127.0.0.1/githome
DATA_DIR=/var/lib/githome
HTML_BASE_URL=https://git.example.com
LISTEN_HTTP=:3000
SESSION_KEY=0102030405060708090a0b0c0d0e0f10...
TOKEN_PEPPER=aabbccddeeff00112233...
LOG_FORMAT=json
WEB_SITE_NAME=My Forge
```

Environment variables in the shell take precedence over values in the config file.

## Example: minimal SQLite (development)

This is enough to run a local instance for development. Session and token secrets are not set, so they are regenerated on every restart (all sessions and tokens are invalidated on each start).

```bash
export GITHOME_DATABASE_URL=sqlite:///tmp/githome-dev.sqlite
export GITHOME_DATA_DIR=/tmp/githome-repos
export GITHOME_HTML_BASE_URL=http://localhost:3000
export GITHOME_ENV=development
export GITHOME_LOG_LEVEL=debug

mkdir -p /tmp/githome-repos
githome serve
```

## Example: PostgreSQL (production)

Production configuration with stable secrets, structured logging, and a PostgreSQL backend:

```bash
# /etc/githome/githome.env

ENV=production
DATABASE_URL=postgres://githome:$(cat /run/secrets/db_password)@pg.internal/githome
DB_POOL_SIZE=20
DATA_DIR=/var/lib/githome
HTML_BASE_URL=https://git.example.com
LISTEN_HTTP=:3000

# Generate once: openssl rand -hex 32
SESSION_KEY=a1b2c3d4e5f6...
# Generate once: openssl rand -hex 16
TOKEN_PEPPER=deadbeef...

LOG_LEVEL=info
LOG_FORMAT=json

WEB_ENABLED=true
WEB_SITE_NAME=Acme Git

SERVER_MAX_BLOB_BYTES=52428800
SHUTDOWN_TIMEOUT=30s
```

Start with:

```bash
GITHOME_CONFIG_FILE=/etc/githome/githome.env githome serve
```

Or with systemd, set `EnvironmentFile=/etc/githome/githome.env` in the unit file and invoke `githome serve` directly.
