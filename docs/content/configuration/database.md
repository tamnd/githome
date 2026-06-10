---
title: "Database"
description: "SQLite and PostgreSQL connection options, pool sizing, migrations, and the git data directory"
weight: 20
---

## Choosing a database

Use SQLite for personal instances or small teams where a single server handles all traffic. Use PostgreSQL when you need multiple application nodes, high write volume, or want to use managed database infrastructure.

Githome does not support running two application nodes against the same SQLite file.

## SQLite

```ini
GITHOME_DATABASE_URL=sqlite:///var/lib/githome/githome.sqlite
```

The path after the third slash is an absolute filesystem path. A relative path (`sqlite://githome.sqlite`) resolves from the working directory.

Githome enables WAL journal mode automatically on the first connection. WAL allows concurrent readers while a write is in progress, which keeps web UI page loads responsive during repository operations.

Write serialization is enforced by setting `max_open_conns=1` for the write connection pool. This prevents `SQLITE_BUSY` errors under concurrent writes without requiring application-level locking. Reads use a separate pool with the default concurrency limit.

There is no additional configuration for SQLite. Githome does not expose journal mode, cache size, or synchronous settings as environment variables.

## PostgreSQL

```ini
GITHOME_DATABASE_URL=postgres://githome:secret@127.0.0.1:5432/githome?sslmode=require
```

The connection string follows the [libpq URI format](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING). Githome uses the `pgx` driver; all parameters that pgx accepts in the DSN are valid here.

Common SSL modes:

| `sslmode` | behavior |
|-----------|----------|
| `disable` | no TLS, plaintext only |
| `require` | TLS required, certificate not verified |
| `verify-ca` | TLS required, server CA verified |
| `verify-full` | TLS required, CA and hostname verified |

For production, use `verify-full` and provide `sslrootcert`:

```ini
GITHOME_DATABASE_URL=postgres://githome:secret@db.internal:5432/githome?sslmode=verify-full&sslrootcert=/etc/ssl/certs/pg-ca.crt
```

### Connection pool size

```ini
GITHOME_DB_POOL_SIZE=10
```

This sets `max_open_conns` for the PostgreSQL pool. The default is 10. Each open connection holds a slot in PostgreSQL's `max_connections` budget; do not set this higher than your database server allows. Idle connections time out after 5 minutes.

## Migrations

Githome applies database migrations automatically at startup, before serving any requests. There is no migration command to run separately.

Migrations are embedded in the binary and run forward only. If the schema is already at the current version, the startup migration step is a no-op.

Downgrade migrations are not provided. To roll back to a previous version of githome, restore the database from a backup taken before the upgrade.

Check the current schema version without starting the server:

```sh
githome db version
```

## Git data directory

```ini
GITHOME_DATA_DIR=/var/lib/githome/repos
```

Githome stores all git bare repositories under this directory. Each repository is placed at `{GITHOME_DATA_DIR}/{pk}/`, where `{pk}` is the repository's integer primary key. This keeps the layout stable even if an owner or repository is renamed.

The data directory must exist and be writable by the githome process before starting. Githome does not create it automatically.

```sh
mkdir -p /var/lib/githome/repos
chown githome:githome /var/lib/githome/repos
```

On the same machine, point both `GITHOME_DATABASE_URL` and `GITHOME_DATA_DIR` at persistent storage. If you run githome in a container, mount both paths as volumes.
