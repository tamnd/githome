---
title: "Operations"
description: "guides for deploying, configuring, and maintaining a githome instance in production"
weight: 5
---

Githome is a single binary with no required runtime dependencies beyond a database and a directory to store git repositories. That simplicity makes it easy to deploy, but there are still production concerns worth getting right: reverse proxying, TLS, backups, and observability.

This section covers the full operational lifecycle:

- **Docker** - running githome in a container, with SQLite or PostgreSQL, using docker-compose or a single `docker run` command.
- **Reverse proxy** - putting nginx, Caddy, or Traefik in front of githome for TLS termination and upload buffering.
- **Backup** - backing up both the database and the git repository data directory, and testing restores.
- **Observability** - health endpoints, structured logs, log shipping, and alerting patterns.

For initial installation and the first-run setup wizard, see the [Installation](/installation) section.

## Quick orientation

Githome has two state components:

1. The **database** (SQLite file or PostgreSQL) - stores users, repos, issues, PRs, and all metadata.
2. The **data directory** (`GITHOME_DATA_DIR`) - stores bare git repositories on disk.

Both must be backed up and both must survive a deployment upgrade. Everything else in githome is stateless.

## Minimal production checklist

- Set `GITHOME_ENV=production`.
- Set `GITHOME_SESSION_KEY` to at least 32 random bytes (do not let githome auto-generate this across restarts).
- Set `GITHOME_TOKEN_PEPPER` to at least 16 random bytes (same warning).
- Set `GITHOME_HTML_BASE_URL` to the exact public URL users see in their browser.
- Put a reverse proxy in front of githome for TLS.
- Schedule automated backups of the database and data directory.
- Ship logs to a central store.
