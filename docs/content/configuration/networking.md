---
title: "Networking"
description: "bind address, public URL, TLS termination, server timeouts, blob size limits, and Unix socket binding"
weight: 30
---

## Listen address

```ini
GITHOME_LISTEN_HTTP=:3000
```

The value is passed directly to Go's `net.Listen`. Use `host:port` to bind to a specific interface, or `:port` to bind on all interfaces.

Bind on localhost only:

```ini
GITHOME_LISTEN_HTTP=127.0.0.1:3000
```

Bind on a Unix domain socket:

```ini
GITHOME_LISTEN_HTTP=unix:/tmp/githome.sock
```

When using a Unix socket with nginx or another reverse proxy, point the proxy's `upstream` at `unix:/tmp/githome.sock` and ensure the proxy process has read/write permission on the socket file.

## Public URL

```ini
GITHOME_HTML_BASE_URL=https://git.example.com
```

This is the URL that users and external systems see. Githome uses it in:

- OAuth redirect URIs
- Git clone URLs returned by the API (`clone_url`, `ssh_url` fields)
- Webhook payload `sender.html_url` and repository URL fields
- Absolute links in server-rendered HTML

Set this to whatever hostname and scheme clients connect to, after any reverse proxy or load balancer. Do not include a trailing slash.

If this is wrong, `git clone` URLs returned by `gh repo clone` will point at the wrong host, and OAuth flows will fail because the redirect URI will not match the registered application.

## TLS

Githome does not terminate TLS itself. Run it behind a reverse proxy such as nginx, Caddy, or a cloud load balancer, and let the proxy handle certificates.

Example nginx configuration:

```nginx
server {
    listen 443 ssl http2;
    server_name git.example.com;

    ssl_certificate     /etc/letsencrypt/live/git.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/git.example.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:3000;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
    }
}
```

With Caddy, TLS with automatic ACME certificates is two lines:

```caddy
git.example.com {
    reverse_proxy 127.0.0.1:3000
}
```

## Server timeouts

These control how long githome waits at each phase of an HTTP connection. All values are Go duration strings (`5s`, `30s`, `2m`).

| Variable | Default | Purpose |
|----------|---------|---------|
| `GITHOME_SERVER_READ_TIMEOUT` | `15s` | Time to read the entire request, including body |
| `GITHOME_SERVER_WRITE_TIMEOUT` | `30s` | Time to write the entire response |
| `GITHOME_SERVER_IDLE_TIMEOUT` | `120s` | Keep-alive idle time between requests on one connection |
| `GITHOME_SERVER_READ_HEADER_TIMEOUT` | `5s` | Time to read request headers before reading the body |

The read and write timeouts reset per request on persistent connections. `GITHOME_SERVER_IDLE_TIMEOUT` applies between requests on the same connection.

For large repository operations like `git push` with a slow client, increase `GITHOME_SERVER_WRITE_TIMEOUT`. The default 30s is intentionally conservative; a value of `120s` is reasonable for most self-hosted setups.

```ini
GITHOME_SERVER_WRITE_TIMEOUT=120s
```

## Blob size limit

```ini
GITHOME_SERVER_MAX_BLOB_BYTES=10485760
```

Maximum number of bytes githome will send for a single blob download through the contents API. The default is 10 MiB (10485760 bytes). Requests for blobs larger than this limit return `HTTP 403` with a message indicating the file must be fetched via git or the raw download endpoint.

Increase this limit if your repositories contain large binary files that clients need to fetch through the API:

```ini
GITHOME_SERVER_MAX_BLOB_BYTES=52428800   # 50 MiB
```

Setting it to `0` disables the limit entirely, which is not recommended on public-facing instances.

## Image proxy (Camo)

These two variables are optional and must be set together:

```ini
GITHOME_MARKUP_CAMO_SECRET=<shared-hmac-secret>
GITHOME_MARKUP_CAMO_BASE_URL=https://camo.example.com
```

When both are set, githome rewrites external image URLs in rendered Markdown through the Camo image proxy. This prevents clients from leaking their IP addresses to third-party image hosts and removes mixed-content warnings on HTTPS sites.

Run a compatible Camo proxy, for example [atmos/camo](https://github.com/atmos/camo), and set the same HMAC secret in both githome and the proxy.
