---
title: "Overview"
description: "config precedence, env file format, secrets auto-generation, and startup validation"
weight: 10
---

## Precedence

Githome resolves configuration in this order, with later sources winning:

1. Built-in defaults
2. Env file (`.env` in the working directory, or the path in `GITHOME_CONFIG_FILE`)
3. Process environment variables

This means a variable set in the shell always overrides the env file, and the env file always overrides compiled defaults.

## Env file format

The env file uses plain `KEY=VALUE` syntax. Quotes are optional. Lines starting with `#` are comments. Blank lines are ignored.

```ini
# githome.env
GITHOME_DATABASE_URL=sqlite:///var/lib/githome/githome.sqlite
GITHOME_DATA_DIR=/var/lib/githome/repos
GITHOME_HTML_BASE_URL=https://git.example.com
GITHOME_LISTEN_HTTP=:3000
GITHOME_ENV=production
GITHOME_LOG_LEVEL=info
GITHOME_LOG_FORMAT=json
```

Point githome at a custom env file path:

```sh
export GITHOME_CONFIG_FILE=/etc/githome/production.env
githome serve
```

## Minimal SQLite configuration

This is enough to start a working server with a local SQLite database:

```ini
GITHOME_DATABASE_URL=sqlite:///var/lib/githome/githome.sqlite
GITHOME_DATA_DIR=/var/lib/githome/repos
GITHOME_HTML_BASE_URL=http://localhost:3000
```

Run it:

```sh
githome serve
```

## Minimal PostgreSQL configuration

```ini
GITHOME_DATABASE_URL=postgres://githome:secret@127.0.0.1:5432/githome?sslmode=require
GITHOME_DATA_DIR=/var/lib/githome/repos
GITHOME_HTML_BASE_URL=https://git.example.com
GITHOME_DB_POOL_SIZE=10
```

## Auto-generated secrets

`GITHOME_SESSION_KEY` and `GITHOME_TOKEN_PEPPER` are cryptographically sensitive. If either is absent, githome generates a random value at startup and logs a warning:

```
WARN  secret not set, generated ephemeral value  key=SESSION_KEY
```

An ephemeral value means sessions and tokens are invalidated every restart. For production, generate stable values and store them in the env file or a secrets manager:

```sh
# generate a 32-byte hex SESSION_KEY
openssl rand -hex 32

# generate a 16-byte hex TOKEN_PEPPER
openssl rand -hex 16
```

Then set them:

```ini
GITHOME_SESSION_KEY=a3f8...64 hex chars...
GITHOME_TOKEN_PEPPER=b1c2...32 hex chars...
```

Githome accepts both hex-encoded strings and raw bytes for these two variables.

## Validating configuration at startup

Run `githome config check` to validate all variables without starting the server:

```sh
githome config check
# OK: all required variables are set
# WARN: GITHOME_SESSION_KEY not set, will be auto-generated
```

Exit code is 0 when no required variables are missing. A non-zero exit code means the server would refuse to start.

To print the effective configuration after applying env file and environment overrides:

```sh
githome config dump
```

Secret fields (`SESSION_KEY`, `TOKEN_PEPPER`) are redacted to `[set]` or `[not set]`.
