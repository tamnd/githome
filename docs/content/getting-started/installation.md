---
title: "Installation"
description: "install the githome binary from releases, go install, Docker, or a package manager"
weight: 30
---

githome ships as a single statically linked binary with no runtime dependencies. Pick whichever installation method fits your environment.

## System requirements

| Mode | CPU | RAM | Notes |
|------|-----|-----|-------|
| SQLite (default) | 1 core | 256 MB | Recommended for single-team or evaluation use |
| Postgres | 2 cores | 512 MB | Required for concurrent write-heavy workloads |

Disk: the binary is under 30 MB. Data directory size depends on your repositories.

## Verify any installation

After installing by any method, run:

```bash
githome --version
```

Expected output:

```
githome version 0.1.2 (commit abc1234) built 2026-01-15
```

---

## Binary downloads

Pre-built binaries are published to the GitHub Releases page for every tagged version:

```
https://github.com/tamnd/githome/releases
```

The release archives follow the pattern `githome_{version}_{os}_{arch}.tar.gz` on Linux and macOS, and `githome_{version}_{os}_{arch}.zip` on Windows.

### Linux amd64

```bash
VERSION="0.1.2"
curl -fsSL "https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_linux_amd64.tar.gz" \
  | tar -xz githome
sudo mv githome /usr/local/bin/githome
```

### Linux arm64

```bash
VERSION="0.1.2"
curl -fsSL "https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_linux_arm64.tar.gz" \
  | tar -xz githome
sudo mv githome /usr/local/bin/githome
```

### macOS (Apple Silicon)

```bash
VERSION="0.1.2"
curl -fsSL "https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_darwin_arm64.tar.gz" \
  | tar -xz githome
sudo mv githome /usr/local/bin/githome
```

### macOS (Intel)

```bash
VERSION="0.1.2"
curl -fsSL "https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_darwin_amd64.tar.gz" \
  | tar -xz githome
sudo mv githome /usr/local/bin/githome
```

### Windows amd64

Download from the releases page and extract the `.zip`:

```powershell
$VERSION = "0.1.2"
Invoke-WebRequest `
  -Uri "https://github.com/tamnd/githome/releases/download/v$VERSION/githome_${VERSION}_windows_amd64.zip" `
  -OutFile "githome.zip"
Expand-Archive githome.zip -DestinationPath .
Move-Item githome.exe C:\tools\githome.exe
```

Add `C:\tools` to your `PATH` if it is not already there.

---

## go install

If you have Go 1.22 or later installed, you can build and install the latest release directly from source:

```bash
go install github.com/tamnd/githome/cmd/githome@latest
```

This places the binary in `$GOPATH/bin` (usually `~/go/bin`). Ensure that directory is in your `PATH`:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

Install a specific version:

```bash
go install github.com/tamnd/githome/cmd/githome@v0.1.2
```

---

## Docker

Pull and run the latest image:

```bash
docker run --rm \
  -p 3000:3000 \
  -v githome-data:/var/lib/githome \
  -e GITHOME_DATABASE_URL="sqlite:///var/lib/githome/githome.sqlite" \
  -e GITHOME_DATA_DIR="/var/lib/githome/repos" \
  -e GITHOME_HTML_BASE_URL="http://localhost:3000" \
  ghcr.io/tamnd/githome:latest
```

Image tags:

| Tag | Meaning |
|-----|---------|
| `latest` | Most recent release |
| `0.1.2` | Pinned release |
| `main` | Built from the main branch (development, not for production) |

Note: the image tag omits the `v` prefix. Use `:0.1.2`, not `:v0.1.2`.

For a persistent setup, use Docker Compose as shown in the [Quick start](../quick-start/).

---

## Homebrew

githome publishes a Homebrew tap. Install with:

```bash
brew tap tamnd/tap
brew install githome
```

Update to the latest version:

```bash
brew upgrade githome
```

The formula installs the binary to `/usr/local/bin/githome` on Intel Macs and `/opt/homebrew/bin/githome` on Apple Silicon. Both are on the default `PATH` after a standard Homebrew setup.

---

## Package managers

GoReleaser produces `.deb`, `.rpm`, and `.apk` packages for each release. These are attached to the GitHub release and also published to the project's package repository.

### Debian / Ubuntu

```bash
VERSION="0.1.2"
curl -fsSL "https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_linux_amd64.deb" \
  -o githome.deb
sudo dpkg -i githome.deb
```

### RHEL / Fedora / Amazon Linux

```bash
VERSION="0.1.2"
curl -fsSL "https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_linux_amd64.rpm" \
  -o githome.rpm
sudo rpm -i githome.rpm
```

### Alpine Linux

```bash
VERSION="0.1.2"
curl -fsSL "https://github.com/tamnd/githome/releases/download/v${VERSION}/githome_${VERSION}_linux_amd64.apk" \
  -o githome.apk
sudo apk add --allow-untrusted githome.apk
```

---

## Running as a system service

After installing the binary, create a systemd unit file for production use on Linux:

```ini
# /etc/systemd/system/githome.service
[Unit]
Description=githome git forge
After=network.target

[Service]
Type=simple
User=githome
Group=githome
EnvironmentFile=/etc/githome/env
ExecStart=/usr/local/bin/githome serve
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Create the environment file at `/etc/githome/env`:

```bash
GITHOME_DATABASE_URL=sqlite:///var/lib/githome/githome.sqlite
GITHOME_DATA_DIR=/var/lib/githome/repos
GITHOME_HTML_BASE_URL=https://git.example.com
GITHOME_LISTEN_HTTP=:3000
GITHOME_ENV=production
GITHOME_LOG_FORMAT=json
```

Create the system user and data directory, then enable the service:

```bash
sudo useradd --system --home /var/lib/githome --shell /usr/sbin/nologin githome
sudo mkdir -p /var/lib/githome/repos
sudo chown -R githome:githome /var/lib/githome
sudo systemctl daemon-reload
sudo systemctl enable --now githome
```

Verify it is running:

```bash
sudo systemctl status githome
curl -s http://localhost:3000/healthz
```

---

## Next steps

- Follow the [Quick start](../quick-start/) to create your first repository and push commits.
- See **Configuration** for the full `GITHOME_*` environment variable reference.
- See **Reverse proxy** for putting githome behind nginx or Caddy with automatic TLS.
