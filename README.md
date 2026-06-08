# Githome

Githome is a self-hosted software forge that speaks GitHub. The goal is narrow and uncompromising: an unmodified `gh`, an unmodified `git`, and any client that talks to the GitHub REST or GraphQL API should work against a Githome server the same way they work against github.com, byte for byte where it matters.

That means the compatibility target is not "GitHub-like". It is GitHub:

- **REST API v3** under `/api/v3`, with the same JSON shapes, the same error envelopes, the same headers (`Link` pagination, `ETag`, the rate-limit family, `X-GitHub-Request-Id`, `X-GitHub-Api-Version`).
- **GraphQL API v4** under `/api/graphql`, with a schema that matches field for field, Relay-style connections, and the `node`/`databaseId`/`id` triple.
- **The `gh` CLI**, unmodified, pointed at a Githome host through `GH_HOST`. If a flow works on github.com it works here, including `--json` field selection and `--paginate`.
- **Git transport** over smart-HTTP, so `git clone` and `git push` go straight to the server.
- **A built-in web UI**, served from the same binary and on by default: repo and code browsing, Markdown with syntax highlighting, issues, pull requests with the diff, inline code review, search, profiles, and settings. It is a reimplemented Primer look, server-rendered, and the write paths work with JavaScript disabled.

## Status

Usable today, and honest about the edges. The compatibility surface is built and released: three tagged versions are out, and one tag push produces the prebuilt binaries, the Linux packages, and the container image attached to each release (see [Install](#install)).

| Version | What it added |
| --- | --- |
| `v0.1.0` | The first complete forge: auth, git read and push, issues, pull requests, code review, webhooks, events, search, conditional requests, the full REST and GraphQL surface |
| `v0.1.1` | A performance pass: batch loaders instead of N+1, keyset pagination, FTS-backed search, object caches, GraphQL dataloaders |
| `v0.1.2` | The release pipeline: archives, deb/rpm/apk, a multi-arch container image, and the language-agnostic package managers |

What works, end to end:

- **Authentication.** Classic PATs with a prefix and checksum, the OAuth device and web flows, scopes on every response header, only the token hash stored.
- **Repositories and git.** `gh repo` operations, `git clone`, `git fetch`, and `git push` over smart-HTTP, with a post-receive sync that keeps the database consistent with what git wrote.
- **Issues, pull requests, and code review.** The full lifecycle over REST and GraphQL, async mergeable state, squash/merge/rebase, inline review threads, commit statuses, check runs, `reviewDecision`, and `statusCheckRollup`.
- **Webhooks and the Events API**, with `X-Hub-Signature-256` delivery, an SSRF guard, and at-least-once retry.
- **Search, pagination, and conditional requests**, with FTS-backed search, RFC 5988 `Link` headers, and `ETag`/`If-None-Match` that returns 304 without spending rate limit.
- **The web UI** described above.

Not yet:

- SSH git transport (the config is reserved, the server is not wired)
- Organization accounts (everything is user-owned for now)
- At-rest encryption of webhook secrets
- GitHub Actions runner compatibility

The definition of done is mechanical, not a claim. Every endpoint has a recorded fixture and a contract test; real `gh` and real `git` drive the server in CI; a JSON differ compares responses against recorded github.com output field for field; and the suite runs against both SQLite and Postgres. The `githome-conform` tool certifies a running instance against the compatibility matrix as a black-box client and exits non-zero on any drift, so it can gate a release.

## Design in one screen

- One Go binary, `githome`, that serves the REST and GraphQL APIs, the git smart-HTTP transport, and the web UI. Two helpers ship beside it: `githome-migrate` runs the schema migrations, and `githome-conform` certifies a running instance against the compatibility matrix.
- Postgres is the primary datastore; SQLite (pure Go, no cgo) is supported for small and single-node deployments. The same migrations run on both, and the server applies them on startup.
- Git access is hybrid: go-git for cheap reads, the `git` binary for transport, merge, and diff.
- One rule keeps the API surfaces from drifting apart: the REST, GraphQL, and web layers never touch the store or git directly. They all go through the domain layer for data and a presenter layer for rendering, so no surface can diverge from another or from GitHub.

There are no Go `internal/` directories in this project on purpose; boundaries are enforced by the single-writer domain layer and by lint, not by the package system.

## Install

Tagged releases ship as static binaries for Linux, macOS, Windows and the BSDs, as Linux packages (deb, rpm, apk), and as a multi-arch container image. The paths that work today:

```sh
docker pull ghcr.io/tamnd/githome:latest                # container
go install github.com/tamnd/githome/cmd/githome@latest  # from source
```

Every release also attaches prebuilt archives and `deb`/`rpm`/`apk` packages you can download and install directly. The pipeline additionally builds Homebrew, Scoop, winget, AUR and Nix entries; those go live once their taps are created and their tokens are set, which [INSTALL.md](INSTALL.md) walks through alongside every install method and the server configuration.

## Run

The server reads its configuration from the environment and applies migrations on startup, so a single-node SQLite instance needs nothing but a few variables:

```sh
export GITHOME_DATABASE_URL="sqlite://githome.db"
export GITHOME_DATA_DIR="./data"
export GITHOME_HTML_BASE_URL="http://localhost:3000"
export GITHOME_SESSION_KEY="$(openssl rand -hex 32)"   # at least 32 bytes
export GITHOME_TOKEN_PEPPER="$(openssl rand -hex 32)"  # at least 16 bytes
githome
```

It listens on `:3000` by default and serves the web UI, the REST API under `/api/v3`, GraphQL under `/api/graphql`, and the git smart-HTTP transport. Point `gh` and `git` at it:

```sh
export GH_HOST=localhost:3000
gh auth login --hostname localhost:3000
gh repo create myrepo --private
git clone http://localhost:3000/<you>/myrepo.git
```

Use a `postgres://` DSN for multi-node or production deployments. Every variable, including the optional ones, is documented in [INSTALL.md](INSTALL.md#configuration) and in the packaged `githome.env`.

## Build

Requires Go 1.26 or newer and a `git` binary on `PATH`.

```sh
make build          # build ./bin/githome and ./bin/githome-migrate
make test           # run the test suite (SQLite)
make test-postgres  # run it against a local Postgres (make pg-up first)
make lint           # golangci-lint v2
make gates          # the cross-cutting CI checks (no internal/, no leaked hosts)
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
