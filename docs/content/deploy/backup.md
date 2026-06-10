---
title: "Backup"
description: "back up the database and git repositories"
weight: 30
---

Two things to back up: the database and `GITHOME_DATA_DIR` where git bare
repos are stored.

## Git repositories

Git repos are plain bare directories. Back them up with rsync or tar:

```sh
rsync -a /var/lib/githome/repos/ backup-host:/backups/githome/repos/

# or as a tarball
tar -czf githome-repos-$(date +%Y%m%d).tar.gz -C /var/lib/githome repos/
```

## SQLite

```sh
# Online backup (safe while githome is running)
sqlite3 /var/lib/githome/githome.sqlite \
  ".backup /backups/githome-$(date +%Y%m%d).sqlite"
```

For continuous replication, use [Litestream](https://litestream.io). It streams
SQLite WAL frames to S3, GCS, or any S3-compatible store in real time with
near-zero overhead.

## PostgreSQL

```sh
pg_dump -U githome githome > /backups/githome-$(date +%Y%m%d).sql
```

Or use `pg_basebackup` for binary backups. A managed Postgres service handles
backups and point-in-time recovery automatically.

## Restore

1. Stop githome: `docker compose down`
2. Restore the database to its configured path
3. Restore `GITHOME_DATA_DIR`
4. Start githome: `docker compose up -d`

Test your restore on a staging instance before you need it in production.
