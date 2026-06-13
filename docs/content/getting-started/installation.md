---
title: "Installation"
description: "download or build githome for your platform"
weight: 30
---

## Binary download

Grab the latest release from
[GitHub Releases](https://github.com/tamnd/githome/releases/latest).

Archive names carry the version, so set it once and download the right asset.
Each archive holds all three binaries (`githome`, `githome-migrate`,
`githome-conform`) plus the license and readme.

```sh
VERSION=0.1.3

# Linux amd64
curl -L https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_linux_amd64.tar.gz | tar xz
sudo mv githome /usr/local/bin/

# macOS arm64 (Apple Silicon)
curl -L https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_darwin_arm64.tar.gz | tar xz
sudo mv githome /usr/local/bin/
```

Verify:

```sh
githome --version
```

## Docker

```sh
docker pull ghcr.io/tamnd/githome:latest
```

Use a pinned tag in production (`0.x.y`), not `latest`.

## Homebrew

```sh
brew install tamnd/tap/githome
```

## Package managers

Every release ships `.deb`, `.rpm`, and `.apk` packages:

```sh
# Debian / Ubuntu
apt install ./githome_*.deb

# RHEL / Fedora
rpm -i githome_*.rpm

# Alpine
apk add --allow-untrusted githome_*.apk
```

## Build from source

```sh
go install github.com/tamnd/githome/cmd/githome@latest
```

Requires Go 1.26+ and a `git` binary on `PATH`.

## Requirements

| Database | CPU | RAM | Disk |
|---|---|---|---|
| SQLite (default) | 1 vCPU | 256 MB | 1 GB+ |
| PostgreSQL | 2 vCPU | 512 MB | 1 GB+ |

githome has no other runtime dependencies. It does not require Node, Python,
Redis, or any external service.
