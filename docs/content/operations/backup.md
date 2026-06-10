---
title: "Backup"
description: "backing up and restoring the githome database and git repository data directory"
weight: 30
---

Githome has two independent state components. Both must be included in every backup:

1. **Database** - all users, repositories, issues, pull requests, and metadata. Either a SQLite file or a PostgreSQL database depending on your `GITHOME_DATABASE_URL`.
2. **Git data directory** (`GITHOME_DATA_DIR`) - the bare git repositories on disk. The database stores metadata; the actual commits, blobs, and refs live here.

A backup of only one component is not usable. A database without its git repos produces 404s on all content. Git repos without the database have no users or metadata.

## SQLite backup

### Option 1: file copy (offline only)

```bash
systemctl stop githome
cp /var/lib/githome/db/githome.sqlite /backup/githome-$(date +%Y%m%d).sqlite
systemctl start githome
```

This is safe only when githome is stopped. Copying a live SQLite file that has outstanding writes produces a corrupt backup.

### Option 2: online backup via sqlite3 (recommended)

```bash
sqlite3 /var/lib/githome/db/githome.sqlite ".backup /backup/githome-$(date +%Y%m%d).sqlite"
```

The `.backup` command uses the SQLite Online Backup API. It is safe to run while githome is running and handles in-progress writes correctly.

### Option 3: WAL checkpoint then copy

If githome is running in WAL journal mode (the default), you can checkpoint and then copy:

```bash
sqlite3 /var/lib/githome/db/githome.sqlite "PRAGMA wal_checkpoint(TRUNCATE);"
cp /var/lib/githome/db/githome.sqlite /backup/githome-$(date +%Y%m%d).sqlite
cp /var/lib/githome/db/githome.sqlite-wal /backup/ 2>/dev/null || true
```

This approach is safe for a consistent copy but requires githome to not be writing during the checkpoint window. The `.backup` command is simpler and preferred for most deployments.

### Automated SQLite backup with cron

```cron
# /etc/cron.d/githome-backup
0 2 * * * root sqlite3 /var/lib/githome/db/githome.sqlite ".backup /backup/db/githome-$(date +\%Y\%m\%d).sqlite" && find /backup/db -name "*.sqlite" -mtime +7 -delete
```

### Continuous replication with Litestream

[Litestream](https://litestream.io) replicates SQLite continuously to S3, GCS, Azure Blob Storage, or SFTP. This gives you near-zero RPO without stopping githome.

```yaml
# /etc/litestream.yml
dbs:
  - path: /var/lib/githome/db/githome.sqlite
    replicas:
      - url: s3://my-bucket/githome/db
        retention: 168h   # 7 days
```

Run litestream alongside githome:

```bash
litestream replicate -config /etc/litestream.yml
```

Restore a specific point in time:

```bash
litestream restore -config /etc/litestream.yml \
  -o /var/lib/githome/db/githome.sqlite \
  s3://my-bucket/githome/db
```

## PostgreSQL backup

### pg_dump (logical backup)

```bash
pg_dump -U githome -h localhost githome | gzip > /backup/db/githome-$(date +%Y%m%d).sql.gz
```

Restore from a logical backup:

```bash
gunzip -c /backup/db/githome-20260610.sql.gz | psql -U githome -h localhost githome
```

### Continuous WAL archiving

For production PostgreSQL, configure `archive_mode = on` and `archive_command` in `postgresql.conf` to ship WAL segments to a durable store. Combined with a base backup via `pg_basebackup`, this gives point-in-time recovery.

```bash
pg_basebackup -U replicator -h localhost -D /backup/pg-base -Ft -z -P
```

Full PostgreSQL PITR setup is outside the scope of this document. The PostgreSQL documentation covers it in detail.

## Git repository backup

```bash
tar -czf /backup/repos/repos-$(date +%Y%m%d).tar.gz \
  -C /var/lib/githome .
```

This backs up all bare git repositories. The archive captures the full directory structure under `GITHOME_DATA_DIR`.

For large installations, rsync to a remote host is more efficient because it transfers only changed objects:

```bash
rsync -az --delete \
  /var/lib/githome/ \
  backup-host:/backup/githome-repos/
```

Run the database backup and the git repos backup close together in time. A database backup from 02:00 paired with a repos backup from 02:01 is fine. A 12-hour gap risks inconsistency if a repository was created in that window.

## Restore procedure

1. Stop githome:

   ```bash
   systemctl stop githome
   ```

2. Restore the database:

   **SQLite:**
   ```bash
   cp /backup/db/githome-20260610.sqlite /var/lib/githome/db/githome.sqlite
   ```

   **PostgreSQL:**
   ```bash
   dropdb -U postgres githome
   createdb -U postgres -O githome githome
   gunzip -c /backup/db/githome-20260610.sql.gz | psql -U githome -h localhost githome
   ```

3. Restore the git data directory:

   ```bash
   rm -rf /var/lib/githome/repos   # or the relevant subdirectory
   tar -xzf /backup/repos/repos-20260610.tar.gz -C /var/lib/githome
   ```

4. Fix ownership if needed:

   ```bash
   chown -R githome:githome /var/lib/githome
   ```

5. Start githome:

   ```bash
   systemctl start githome
   ```

6. Verify:

   ```bash
   curl -s http://localhost:3000/readyz
   curl -s -H "Authorization: Bearer <admin-token>" http://localhost:3000/user
   ```

## Testing backups

A backup you have not restored is not a backup. Schedule a monthly restore test against a staging instance:

1. Spin up a staging githome pointing at a different port and database path.
2. Restore the production backup to staging.
3. Verify via the API that repositories, users, and issues are intact:

   ```bash
   curl -s http://staging:3001/user/repos \
     -H "Authorization: Bearer <test-token>" | jq '.[].full_name'
   ```

4. Clone a representative repository to confirm git object integrity:

   ```bash
   git clone http://staging:3001/alice/myrepo.git /tmp/restore-test
   git -C /tmp/restore-test log --oneline -10
   ```

## Retention policy

A baseline policy for small deployments:

- Daily snapshots: keep 7 days.
- Weekly snapshots (Sunday): keep 4 weeks.
- Monthly snapshots (1st of month): keep 12 months.

Implement this with a cron job that prunes old files after each backup, or use the retention settings in Litestream or your cloud object storage lifecycle rules.
