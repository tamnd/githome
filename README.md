# Githome

Githome is a self-hosted software forge that speaks GitHub. The goal is narrow
and uncompromising: an unmodified `gh`, an unmodified `git`, and any client that
talks to the GitHub REST or GraphQL API should work against a Githome server the
same way they work against github.com, byte for byte where it matters.

That means the compatibility target is not "GitHub-like". It is GitHub:

- **REST API v3** under `/api/v3`, with the same JSON shapes, the same error
  envelopes, the same headers (`Link` pagination, `ETag`, the rate-limit family,
  `X-GitHub-Request-Id`, `X-GitHub-Api-Version`).
- **GraphQL API v4** under `/api/graphql`, with a schema that matches field for
  field, Relay-style connections, and the `node`/`databaseId`/`id` triple.
- **The `gh` CLI**, unmodified, pointed at a Githome host through `GH_HOST`. If a
  flow works on github.com it works here, including `--json` field selection and
  `--paginate`.
- **Git transport**, smart-HTTP and SSH, so `git clone` and `git push` go
  straight to the server.

## Status

Early and honest about it. Githome is being built milestone by milestone, and
each milestone lands as its own pull request with a runnable acceptance gate
before the next one starts. The roadmap is:

| Milestone | Theme | Lands |
| --- | --- | --- |
| M0 | Foundations: config, store, migrations, the REST envelope, `/meta`, `/rate_limit`, health | first |
| M1 | Authentication: tokens, OAuth device + web flow, `gh auth login` |
| M2 | Repositories and git read: clone, browse contents and trees |
| M3 | Git write: push, refs, the post-receive sync |
| M4 | Issues |
| M5 | Pull requests |
| M6 | Code review |
| M7 | Webhooks and events |
| M8 | Hardening and parity: search, conditional requests, conformance |

A milestone is only "done" when its acceptance gate is green in CI and every new
endpoint has a recorded fixture and a passing contract test. The mechanical
definition of done lives in the test suite: real `gh` and real `git` driving the
server, and a JSON differ that compares Githome's output against recorded
github.com responses field for field.

## Design in one screen

- A single Go binary, `githome`, plus a `githome-migrate` helper.
- Postgres is the primary datastore; SQLite (pure Go, no cgo) is supported for
  small and single-node deployments. The same migrations run on both.
- Git access is hybrid: go-git for cheap reads, the `git` binary for transport,
  merge, and diff.
- One rule keeps the two API surfaces from drifting apart: the REST and GraphQL
  layers never touch the store or git directly. Both go through the domain layer
  for data and a presenter layer for rendering, so neither surface can diverge
  from the other or from GitHub.

There are no Go `internal/` directories in this project on purpose; boundaries
are enforced by the single-writer domain layer and by lint, not by the package
system.

## Install

Tagged releases ship as static binaries for Linux, macOS, Windows and the BSDs,
as Linux packages (deb, rpm, apk), and as a multi-arch container image. The
common paths:

```sh
brew install tamnd/tap/githome                       # macOS, Linux
docker pull ghcr.io/tamnd/githome:latest             # container
go install github.com/tamnd/githome/cmd/githome@latest  # from source
```

[INSTALL.md](INSTALL.md) covers every method (Homebrew, Scoop, winget, deb, rpm,
apk, the AUR, Nix, Docker, prebuilt archives) and the server configuration.

## Build

Requires Go 1.26 or newer and a `git` binary on `PATH`.

```sh
make build      # build ./bin/githome and ./bin/githome-migrate
make test       # run the test suite (SQLite by default)
make lint       # golangci-lint v2
```

Running the server and pointing `gh` at it is documented as the milestones that
serve those flows land.

## License

Apache License 2.0. See [LICENSE](LICENSE).
