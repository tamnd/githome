---
title: "Quick start"
description: "go from zero to pushing code in 5 minutes"
weight: 20
---

## Run githome

The fastest path is Docker:

```sh
docker run -d --name githome \
  -p 3000:3000 \
  -v githome-data:/var/lib/githome \
  -e GITHOME_HTML_BASE_URL=http://localhost:3000 \
  -e GITHOME_WEB_ENABLED=true \
  ghcr.io/tamnd/githome:latest
```

Wait for it to be ready:

```sh
curl http://localhost:3000/readyz
# → OK
```

If you prefer a binary:

```sh
# macOS (Apple Silicon)
VERSION=0.1.3
curl -L https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_darwin_arm64.tar.gz | tar xz
GITHOME_HTML_BASE_URL=http://localhost:3000 ./githome
```

## Create your first account

Open http://localhost:3000 and register. The first account becomes the admin.

Generate a personal access token: **Settings > Developer settings > Personal
access tokens > Generate new token**. Copy it, you will use it in the next step.

## Connect the gh CLI

```sh
echo YOUR_TOKEN | gh auth login --hostname localhost:3000 --with-token
gh auth status --hostname localhost:3000
```

Create a repo and push:

```sh
gh repo create myproject --private --hostname localhost:3000
cd myproject
git init && echo "# myproject" > README.md
git add . && git commit -m "init"
git push -u origin main
```

## Clone and push without gh CLI

```sh
git clone http://localhost:3000/alice/myrepo.git
```

For password-less access, store your credentials once:

```sh
git config --global credential.helper store
echo "machine localhost login alice password YOUR_TOKEN" >> ~/.netrc
```

## What is next

- [Connect more tools](/getting-started/connect-your-tools/): VS Code, Octokit, Terraform, CI
- [Deploy to a real server](/deploy/docker/): HTTPS, persistent storage, PostgreSQL
- [All configuration options](/configure/overview/)
