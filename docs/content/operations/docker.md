---
title: "Docker"
description: "running githome in a container using docker run or docker compose"
weight: 10
---

The official image is `ghcr.io/tamnd/githome` and is published for `linux/amd64` and `linux/arm64`.

## Single container quickstart

```bash
docker run -d \
  --name githome \
  -p 3000:3000 \
  -e GITHOME_DATABASE_URL="sqlite:///var/lib/githome/db/githome.sqlite" \
  -e GITHOME_DATA_DIR="/var/lib/githome" \
  -e GITHOME_HTML_BASE_URL="http://localhost:3000" \
  -e GITHOME_SESSION_KEY="$(openssl rand -hex 32)" \
  -e GITHOME_TOKEN_PEPPER="$(openssl rand -hex 16)" \
  -e GITHOME_ENV="production" \
  -e GITHOME_LOG_FORMAT="json" \
  -v githome-data:/var/lib/githome \
  ghcr.io/tamnd/githome:latest
```

Githome creates the SQLite file and runs migrations automatically on startup.

## docker-compose with SQLite

```yaml
services:
  githome:
    image: ghcr.io/tamnd/githome:latest
    restart: unless-stopped
    ports:
      - "3000:3000"
    environment:
      GITHOME_DATABASE_URL: "sqlite:///var/lib/githome/db/githome.sqlite"
      GITHOME_DATA_DIR: "/var/lib/githome"
      GITHOME_HTML_BASE_URL: "https://git.example.com"
      GITHOME_LISTEN_HTTP: ":3000"
      GITHOME_SESSION_KEY: "${GITHOME_SESSION_KEY}"
      GITHOME_TOKEN_PEPPER: "${GITHOME_TOKEN_PEPPER}"
      GITHOME_ENV: "production"
      GITHOME_LOG_LEVEL: "info"
      GITHOME_LOG_FORMAT: "json"
      GITHOME_WEB_ENABLED: "true"
      GITHOME_WEB_SITE_NAME: "Githome"
      GITHOME_SERVER_READ_TIMEOUT: "15s"
      GITHOME_SERVER_WRITE_TIMEOUT: "30s"
      GITHOME_SERVER_IDLE_TIMEOUT: "120s"
      GITHOME_SERVER_READ_HEADER_TIMEOUT: "5s"
      GITHOME_SERVER_MAX_BLOB_BYTES: "10485760"
      GITHOME_SHUTDOWN_TIMEOUT: "10s"
      GITHOME_DB_POOL_SIZE: "10"
    volumes:
      - ./data:/var/lib/githome
      - ./db:/var/lib/githome/db
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:3000/readyz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
```

Put the secrets in a `.env` file next to your `docker-compose.yml`:

```bash
# .env
GITHOME_SESSION_KEY=<output of: openssl rand -hex 32>
GITHOME_TOKEN_PEPPER=<output of: openssl rand -hex 16>
```

Do not commit `.env` to source control.

## docker-compose with PostgreSQL

```yaml
services:
  postgres:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_USER: githome
      POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"
      POSTGRES_DB: githome
    volumes:
      - pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "githome"]
      interval: 10s
      timeout: 5s
      retries: 5

  githome:
    image: ghcr.io/tamnd/githome:latest
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "3000:3000"
    environment:
      GITHOME_DATABASE_URL: "postgres://githome:${POSTGRES_PASSWORD}@postgres/githome?sslmode=disable"
      GITHOME_DATA_DIR: "/var/lib/githome"
      GITHOME_HTML_BASE_URL: "https://git.example.com"
      GITHOME_SESSION_KEY: "${GITHOME_SESSION_KEY}"
      GITHOME_TOKEN_PEPPER: "${GITHOME_TOKEN_PEPPER}"
      GITHOME_ENV: "production"
      GITHOME_LOG_FORMAT: "json"
      GITHOME_DB_POOL_SIZE: "20"
    volumes:
      - ./data:/var/lib/githome
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:3000/readyz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 15s

volumes:
  pg-data:
```

Migrations run automatically on startup. The `depends_on` health check prevents githome from starting before PostgreSQL accepts connections.

## Named volumes vs bind mounts

Named volumes (e.g. `githome-data:/var/lib/githome`) are managed by Docker and are opaque to the host filesystem. They are easier to snapshot with `docker run --volumes-from` but harder to inspect directly.

Bind mounts (e.g. `./data:/var/lib/githome`) place files at a known host path. This makes backup scripts, `rsync`, and direct inspection straightforward. For most self-hosted deployments bind mounts are the clearer choice.

Use a bind mount for `GITHOME_DATA_DIR` so your git repositories are easy to back up with standard tools.

## Container user and volume permissions

The githome image runs as UID 1000 by default. When using bind mounts, the host directory must be owned or writable by UID 1000:

```bash
mkdir -p ./data ./db
chown -R 1000:1000 ./data ./db
```

To run as a different UID:

```bash
docker run --user 2000:2000 ...
```

Ensure the volume path is owned by that UID before starting the container.

## Secrets management

**Docker secrets (Swarm or Compose v3):**

```yaml
services:
  githome:
    image: ghcr.io/tamnd/githome:latest
    secrets:
      - session_key
      - token_pepper
    environment:
      GITHOME_SESSION_KEY_FILE: "/run/secrets/session_key"
      GITHOME_TOKEN_PEPPER_FILE: "/run/secrets/token_pepper"

secrets:
  session_key:
    file: ./secrets/session_key.txt
  token_pepper:
    file: ./secrets/token_pepper.txt
```

The `_FILE` suffix variant reads the secret from a file path, which keeps the value out of `docker inspect` output.

**Environment file approach:**

```bash
docker run --env-file /etc/githome/secrets.env ...
```

Keep `/etc/githome/secrets.env` readable only by root or the deploy user (`chmod 600`).

## Building from source

```bash
git clone https://github.com/tamnd/githome.git
cd githome
docker build -t githome:local .
```

The `Dockerfile` performs a multi-stage build. The final image contains only the compiled binary and static assets.

## Upgrading

Migrations run automatically when githome starts, so upgrading is:

```bash
docker compose pull
docker compose up -d
```

Githome performs a rolling restart. Check the health endpoint after the upgrade:

```bash
curl -s http://localhost:3000/readyz
```

A 200 response confirms the new version is running and migrations completed successfully. If the health check returns 503, check logs with `docker compose logs githome`.

## Multi-platform images

The published image supports both `linux/amd64` and `linux/arm64`. Docker pulls the correct variant automatically. To build for a specific platform:

```bash
docker buildx build --platform linux/arm64 -t githome:arm64 .
```
