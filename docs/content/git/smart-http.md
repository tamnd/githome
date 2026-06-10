---
title: "Smart HTTP"
description: "cloning, pushing, and authenticating over the Git Smart HTTP protocol"
weight: 10
---

Githome implements the [Git Smart HTTP protocol](https://www.git-scm.com/docs/http-protocol).
Git clients negotiate capabilities and exchange pack data over four HTTP endpoints:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/{owner}/{repo}.git/info/refs?service=git-upload-pack` | GET | advertise refs for clone/fetch |
| `/{owner}/{repo}.git/git-upload-pack` | POST | send pack data on clone/fetch |
| `/{owner}/{repo}.git/info/refs?service=git-receive-pack` | GET | advertise refs for push |
| `/{owner}/{repo}.git/git-receive-pack` | POST | receive pack data on push |

Git discovers these automatically; you never call them directly.

## Clone URL format

```
http://HOST/{owner}/{repo}.git
https://HOST/{owner}/{repo}.git
```

Both the `.git` suffix and its absence work. Example with a local server:

```bash
git clone http://localhost:3000/alice/myrepo.git
```

## Authentication

All four transport endpoints require authentication for private repositories.
Public repositories allow unauthenticated clone and fetch; push always requires
a valid token.

### URL credentials

Embed a personal access token directly in the URL:

```bash
git clone http://alice:TOKEN@localhost:3000/alice/myrepo.git
```

Git stores the URL, including the embedded credentials, in `.git/config`.
This is convenient for scripts and CI environments where the token is already
available as an environment variable:

```bash
git clone http://alice:${GITHOME_TOKEN}@localhost:3000/alice/myrepo.git
```

### git credential helper (recommended for interactive use)

The `store` helper writes credentials to `~/.git-credentials` in plain text.
Use it when you want git to prompt once and remember the token:

```bash
git config --global credential.helper store
git clone http://localhost:3000/alice/myrepo.git
# git prompts for username and password; enter your token as the password
```

The `osxkeychain` helper on macOS stores credentials in the system keychain:

```bash
git config --global credential.helper osxkeychain
```

Set the credential helper per-host to avoid mixing up credentials on machines
with multiple git hosts:

```bash
git config --global credential.https://git.example.com.helper store
```

### .netrc file for automation

Create or append to `~/.netrc`:

```
machine localhost login alice password TOKEN
```

For a custom hostname:

```
machine git.example.com login alice password TOKEN
```

Set permissions so only your user can read it:

```bash
chmod 600 ~/.netrc
```

Git reads `.netrc` automatically; no credential helper configuration is needed.

## gh CLI integration

Authenticate the `gh` CLI against a self-hosted githome server:

```bash
gh auth login --hostname localhost:3000 --git-protocol http
```

`gh` prompts for a token and configures both the API client and the git
credential helper for that hostname. After this, `gh repo clone alice/myrepo`
and `git push` work without further credential setup.

To verify the auth state:

```bash
gh auth status --hostname localhost:3000
```

## Configuring a remote after cloning

If you cloned without credentials and want to add them later:

```bash
git remote set-url origin http://alice:TOKEN@localhost:3000/alice/myrepo.git
```

Or leave the URL clean and rely on the credential helper:

```bash
git remote set-url origin http://localhost:3000/alice/myrepo.git
git config credential.helper store
```

## Push configuration

Set push behavior to avoid ambiguous pushes:

```bash
git config push.default simple
```

With `simple`, `git push` pushes the current branch to its upstream tracking
branch only, which is the safest default on servers that mirror GitHub behavior.

## TLS termination

Githome does not terminate TLS itself. Run it behind a reverse proxy that
handles certificates. Example Caddy configuration:

```
git.example.com {
    reverse_proxy localhost:3000
}
```

Example nginx block:

```nginx
server {
    listen 443 ssl;
    server_name git.example.com;

    ssl_certificate     /etc/ssl/certs/git.example.com.crt;
    ssl_certificate_key /etc/ssl/keys/git.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # Smart HTTP requires no request body size limit for push
        client_max_body_size 0;
    }
}
```

After setting up the proxy, use `https://` URLs:

```bash
git clone https://alice:TOKEN@git.example.com/alice/myrepo.git
```

## Blob download limit

`GITHOME_SERVER_MAX_BLOB_BYTES` (default 10 MiB) limits how many bytes the
Contents API returns for a single blob via `GET /repos/{owner}/{repo}/git/blobs/{sha}`.
This limit does not apply to git push or clone; pack data flows through the
upload-pack and receive-pack endpoints without a size cap.

## Git LFS

Git LFS is not supported. Attempting to push LFS pointer files will push the
pointer text only; the LFS batch API (`POST /objects/batch`) is not implemented.
Store large binary assets outside the repository or use an external LFS server.
