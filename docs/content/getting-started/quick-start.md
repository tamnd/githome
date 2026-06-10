---
title: "Quick start"
description: "run githome with Docker Compose and make your first git push in five minutes"
weight: 20
---

This guide gets a githome instance running on your laptop and walks through the complete workflow: start the server, create a repository, push commits, and verify the result through the REST API.

## Prerequisites

Choose one path:

- **Docker path:** Docker Engine 24+ and Docker Compose v2.
- **Go path:** Go 1.22+ installed and `$GOPATH/bin` in your `PATH`.

## Docker Compose

Create a working directory and add this `docker-compose.yml`:

```yaml
services:
  githome:
    image: ghcr.io/tamnd/githome:latest
    ports:
      - "3000:3000"
    volumes:
      - githome-data:/var/lib/githome
      - githome-db:/var/lib/githome/db
    environment:
      GITHOME_DATABASE_URL: sqlite:///var/lib/githome/db/githome.sqlite
      GITHOME_DATA_DIR: /var/lib/githome/repos
      GITHOME_HTML_BASE_URL: http://localhost:3000
      GITHOME_LISTEN_HTTP: ":3000"
      GITHOME_WEB_ENABLED: "true"
      GITHOME_WEB_SITE_NAME: "My Githome"
      GITHOME_ENV: development
      GITHOME_LOG_LEVEL: info
      GITHOME_LOG_FORMAT: text

volumes:
  githome-data:
  githome-db:
```

Start it:

```bash
docker compose up -d
```

Wait a few seconds for the database to initialize, then confirm the server is up:

```bash
curl -s http://localhost:3000/healthz
```

Expected output: `ok`

## Go path alternative

If you have Go installed and prefer not to use Docker:

```bash
go install github.com/tamnd/githome/cmd/githome@latest

export GITHOME_DATABASE_URL="sqlite:///tmp/githome.sqlite"
export GITHOME_DATA_DIR="/tmp/githome-repos"
export GITHOME_HTML_BASE_URL="http://localhost:3000"
export GITHOME_WEB_ENABLED="true"

githome serve
```

The rest of this guide works identically for both paths.

## Create the first user

Open `http://localhost:3000` in a browser. The web UI serves a registration form on the first visit. Create a user named `alice` with a password of your choice.

Alternatively, use the API directly. The first `POST /users` call when no users exist creates an admin account:

```bash
curl -s -X POST http://localhost:3000/users \
  -H 'Content-Type: application/json' \
  -d '{"login":"alice","password":"changeme","email":"alice@example.com"}' \
  | jq .login
```

Expected: `"alice"`

## Create a personal access token

From the web UI, navigate to **Settings > Developer settings > Personal access tokens > Generate new token**. Copy the token value; it is only shown once.

Using the API:

```bash
curl -s -X POST http://localhost:3000/user/tokens \
  -u alice:changeme \
  -H 'Content-Type: application/json' \
  -d '{"note":"quick-start","scopes":["repo","read:user"]}' \
  | jq .token
```

Store the token in a shell variable for the rest of this guide:

```bash
TOKEN="ghp_your_token_here"
```

## Verify authentication

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:3000/user \
  | jq '{login, email}'
```

Expected:

```json
{
  "login": "alice",
  "email": "alice@example.com"
}
```

## Create a repository

```bash
curl -s -X POST http://localhost:3000/user/repos \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"hello","description":"Quick start repo","private":false,"auto_init":true}' \
  | jq '{full_name, clone_url, default_branch}'
```

Expected:

```json
{
  "full_name": "alice/hello",
  "clone_url": "http://localhost:3000/alice/hello.git",
  "default_branch": "main"
}
```

## Clone and push

Clone using your token as the HTTP password:

```bash
git clone http://alice:$TOKEN@localhost:3000/alice/hello.git
cd hello
```

Add a file and push:

```bash
echo "# Hello from githome" >> README.md
git add README.md
git commit -m "Add README"
git push origin main
```

Expected output from git:

```
Enumerating objects: 5, done.
Counting objects: 100% (5/5), done.
Writing objects: 100% (3/3), 312 bytes | 312.00 KiB/s, done.
To http://localhost:3000/alice/hello.git
   a1b2c3d..e4f5a6b  main -> main
```

## Verify through the API

Check the latest commit:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:3000/repos/alice/hello/commits \
  | jq '.[0] | {sha: .sha, message: .commit.message, author: .commit.author.name}'
```

Get the file content:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:3000/repos/alice/hello/contents/README.md" \
  | jq '{name, path, size}' 
```

Get the repository metadata:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:3000/repos/alice/hello \
  | jq '{full_name, default_branch, open_issues_count, stargazers_count}'
```

## Try the gh CLI

If you have `gh` installed, point it at your githome instance:

```bash
export GH_HOST=localhost:3000
export GH_TOKEN=$TOKEN

gh repo view alice/hello
gh issue create --repo alice/hello --title "First issue" --body "Opened via gh CLI"
gh issue list --repo alice/hello
```

## Next steps

- See [Installation](../installation/) for production deployment options including binary downloads, Homebrew, and distro packages.
- See **Configuration** for the full list of `GITHOME_*` environment variables including Postgres, timeouts, and image proxy settings.
- See **Reverse proxy** for putting githome behind nginx or Caddy with TLS.
