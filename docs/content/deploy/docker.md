---
title: "Docker deployment"
description: "run githome in production with Docker Compose"
weight: 10
---

## Compose file

Create `docker-compose.yml`:

```yaml
services:
  githome:
    image: ghcr.io/tamnd/githome:0.1.2  # pin to a release tag
    restart: unless-stopped
    ports:
      - "127.0.0.1:3000:3000"   # loopback only; nginx/Caddy handles TLS
    volumes:
      - githome-data:/var/lib/githome
    environment:
      GITHOME_HTML_BASE_URL: https://git.example.com
      GITHOME_DATABASE_URL: sqlite:///var/lib/githome/githome.sqlite
      GITHOME_WEB_ENABLED: "true"
      GITHOME_ENV: production
      GITHOME_LOG_FORMAT: json
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:3000/readyz"]
      interval: 30s
      timeout: 5s
      retries: 3

volumes:
  githome-data:
```

Start:

```sh
docker compose up -d
docker compose logs -f githome
```

## With PostgreSQL

Add a Postgres service and change the database URL:

```yaml
services:
  githome:
    image: ghcr.io/tamnd/githome:0.1.2
    restart: unless-stopped
    ports:
      - "127.0.0.1:3000:3000"
    volumes:
      - githome-data:/var/lib/githome
    environment:
      GITHOME_HTML_BASE_URL: https://git.example.com
      GITHOME_DATABASE_URL: postgres://githome:secret@db/githome?sslmode=disable
      GITHOME_WEB_ENABLED: "true"
      GITHOME_ENV: production
    depends_on:
      db:
        condition: service_healthy

  db:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: githome
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: githome
    volumes:
      - pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "githome"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  githome-data:
  pg-data:
```

## Upgrading

```sh
# Edit docker-compose.yml to update the image tag, then:
docker compose pull
docker compose up -d
```

Migrations run automatically on startup. Back up your database before upgrading.
