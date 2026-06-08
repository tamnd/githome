# Installing Githome

Githome ships as static, pure-Go binaries (the server `githome` plus the
operator tools `githome-migrate` and `githome-conform`). Every artifact below
comes out of one tagged release, built by [GoReleaser](https://goreleaser.com)
from [`.goreleaser.yaml`](.goreleaser.yaml).

Two notes that apply to all methods:

- The server shells out to a `git` binary for the smart-HTTP transport, so keep
  `git` on `PATH` (the container image and the Linux packages pull it in for
  you).
- After installing, configure the server before starting it. The Linux packages
  drop a commented `/etc/githome/githome.env`; for other methods, set the same
  environment variables yourself. See [Configuration](#configuration).

## Contents

- [Homebrew (macOS)](#homebrew-macos)
- [Scoop / winget (Windows)](#scoop--winget-windows)
- [Debian / Ubuntu (.deb)](#debian--ubuntu-deb)
- [Fedora / RHEL / openSUSE (.rpm)](#fedora--rhel--opensuse-rpm)
- [Alpine (.apk)](#alpine-apk)
- [Arch Linux (AUR)](#arch-linux-aur)
- [Nix](#nix)
- [Docker](#docker)
- [Go install](#go-install)
- [Prebuilt archives](#prebuilt-archives)
- [Configuration](#configuration)
- [For maintainers: wiring up each package manager](#for-maintainers-wiring-up-each-package-manager)

## Homebrew (macOS)

```sh
brew install tamnd/tap/githome
```

Githome ships as a Homebrew cask, which is macOS-only. On Linux, use the deb,
rpm or apk package, the container image, or `go install`.

## Scoop / winget (Windows)

```powershell
scoop bucket add tamnd https://github.com/tamnd/scoop-bucket
scoop install githome
```

```powershell
winget install tamnd.githome
```

## Debian / Ubuntu (.deb)

Download the `.deb` for your architecture from the
[latest release](https://github.com/tamnd/githome/releases/latest) and install
it:

```sh
sudo apt install ./githome_*_linux_amd64.deb
```

The package installs the binaries, a `githome.service` systemd unit, and a
commented `/etc/githome/githome.env`. Edit the env file, then:

```sh
sudo systemctl enable --now githome
```

## Fedora / RHEL / openSUSE (.rpm)

```sh
sudo dnf install ./githome_*_linux_amd64.rpm   # or: sudo zypper install ...
sudo systemctl enable --now githome
```

## Alpine (.apk)

```sh
sudo apk add --allow-untrusted ./githome_*_linux_amd64.apk
```

Alpine has no systemd, so the `.apk` ships the binaries and the env file only;
run `githome` under your init of choice (OpenRC, supervisord, a container).

## Arch Linux (AUR)

```sh
yay -S githome-bin     # or any AUR helper
```

## Nix

With the Nix user repository configured:

```sh
nix-env -iA nur.repos.tamnd.githome
```

## Docker

```sh
docker pull ghcr.io/tamnd/githome:latest

docker run --rm -p 3000:3000 \
  -v githome-data:/var/lib/githome \
  -e GITHOME_DATABASE_URL=sqlite:///var/lib/githome/githome.sqlite \
  -e GITHOME_DATA_DIR=/var/lib/githome \
  -e GITHOME_HTML_BASE_URL=http://localhost:3000 \
  -e GITHOME_SESSION_KEY="$(openssl rand -hex 32)" \
  -e GITHOME_TOKEN_PEPPER="$(openssl rand -hex 32)" \
  ghcr.io/tamnd/githome:latest
```

The image is multi-arch (`linux/amd64`, `linux/arm64`). Releases are also signed
with [cosign](https://docs.sigstore.dev/) and carry an SBOM.

## Go install

```sh
go install github.com/tamnd/githome/cmd/githome@latest
go install github.com/tamnd/githome/cmd/githome-migrate@latest
```

This builds from source and needs Go 1.26 or newer.

## Prebuilt archives

Every release attaches a `tar.gz` (Linux, macOS, the BSDs) or `zip` (Windows)
per platform, each carrying all three binaries plus the license. A
`checksums.txt` and its cosign signature sit beside them. Download, verify, and
drop the binaries on your `PATH`:

```sh
tar -xzf githome_*_linux_amd64.tar.gz
sudo install githome*/githome /usr/local/bin/
```

## Configuration

The server reads its configuration from the environment. The required variables:

| Variable | Meaning |
| --- | --- |
| `GITHOME_DATABASE_URL` | Metadata store DSN, e.g. `sqlite:///var/lib/githome/githome.sqlite` |
| `GITHOME_DATA_DIR` | Working directory for repositories and on-disk state |
| `GITHOME_HTML_BASE_URL` | Absolute public base URL; must not be a github.com host |
| `GITHOME_SESSION_KEY` | Session secret, at least 32 bytes (`openssl rand -hex 32`) |
| `GITHOME_TOKEN_PEPPER` | Token pepper, at least 16 characters |

Optional: `GITHOME_LISTEN_HTTP` (default `:3000`), `GITHOME_LOG_LEVEL`,
`GITHOME_ENV`. The packaged `/etc/githome/githome.env` documents them all.

## For maintainers: wiring up each package manager

The release pipeline always produces the downloadable artifacts (archives, deb,
rpm, apk) and pushes the container image to GHCR using the built-in workflow
token. The package managers that live in a separate repository self-disable
until their secret is present, so the pipeline never fails for a tap that has
not been set up. To light each one up, create the repository and add the secret:

| Manager | Repository to create | Repository secret |
| --- | --- | --- |
| Homebrew | `tamnd/homebrew-tap` | `HOMEBREW_TAP_GITHUB_TOKEN` (PAT with write to the tap) |
| Scoop | `tamnd/scoop-bucket` | `SCOOP_BUCKET_GITHUB_TOKEN` |
| winget | fork `tamnd/winget-pkgs` | `WINGET_GITHUB_TOKEN` |
| AUR | `githome-bin` on aur.archlinux.org | `AUR_KEY` (the account's private SSH key) |
| Nix | `tamnd/nur` | `NUR_GITHUB_TOKEN` |

GHCR and the GitHub release need no extra secret. Signing is keyless via the
workflow's OIDC token. An apt/yum repository (so users can `apt install githome`
straight from a hosted repo, rather than downloading the `.deb`) is a later
addition; it needs a package host such as [Gemfury](https://gemfury.com) and is
tracked separately.
