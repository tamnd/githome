---
title: "Reverse proxy"
description: "terminating TLS and proxying requests to githome with nginx, Caddy, or Traefik"
weight: 20
---

Githome listens on plain HTTP. In production, put a reverse proxy in front for TLS termination, access logging, and to handle large release asset uploads correctly.

Before configuring any proxy, set:

```bash
GITHOME_HTML_BASE_URL=https://git.example.com
```

This value is used to generate OAuth redirect URIs, clone URLs, and webhook URLs. If it does not match the URL users actually reach, OAuth flows break and clone URLs point nowhere.

## nginx

### Full configuration

```nginx
upstream githome {
    server 127.0.0.1:3000;
    keepalive 32;
}

server {
    listen 80;
    server_name git.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name git.example.com;

    ssl_certificate     /etc/letsencrypt/live/git.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/git.example.com/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    # Allow large release asset uploads (set this to your expected max asset size)
    client_max_body_size 512m;

    location / {
        proxy_pass         http://githome;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header   Connection "";
        proxy_read_timeout 300s;
    }

    # Disable request buffering for release asset uploads
    location /api/uploads/ {
        proxy_pass                  http://githome;
        proxy_request_buffering     off;
        proxy_set_header            Host              $host;
        proxy_set_header            X-Real-IP         $remote_addr;
        proxy_set_header            X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header            X-Forwarded-Proto $scheme;
        proxy_http_version          1.1;
        proxy_set_header            Connection "";
        proxy_read_timeout          600s;
        client_max_body_size        512m;
    }
}
```

The `/api/uploads/` block disables request buffering so that nginx streams large uploads directly to githome rather than writing the entire body to disk first. Without this, uploading a multi-hundred-megabyte release asset consumes nginx's temp space and adds latency.

### Testing the configuration

```bash
nginx -t
systemctl reload nginx
```

Verify that a git clone works over HTTPS:

```bash
git clone https://git.example.com/alice/myrepo.git
```

## Caddy

Caddy is the fastest path to automatic HTTPS via Let's Encrypt.

```caddyfile
git.example.com {
    reverse_proxy localhost:3000

    # Increase body limit for release asset uploads
    request_body {
        max_size 512MB
    }

    # Disable buffering for the upload endpoint
    @uploads path /api/uploads/*
    handle @uploads {
        reverse_proxy localhost:3000 {
            flush_interval -1
        }
    }

    log {
        output file /var/log/caddy/githome-access.log
        format json
    }
}
```

Caddy obtains and renews TLS certificates automatically. No certificate management is required beyond pointing DNS at the server.

Start or reload Caddy:

```bash
systemctl reload caddy
```

Caddy reads `Caddyfile` from `/etc/caddy/Caddyfile` by default.

## Traefik (Docker labels)

When running githome with docker compose, Traefik can pick up routing configuration from container labels.

```yaml
services:
  traefik:
    image: traefik:v3
    command:
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--entrypoints.web.address=:80"
      - "--entrypoints.websecure.address=:443"
      - "--certificatesresolvers.le.acme.tlschallenge=true"
      - "--certificatesresolvers.le.acme.email=admin@example.com"
      - "--certificatesresolvers.le.acme.storage=/letsencrypt/acme.json"
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - letsencrypt:/letsencrypt

  githome:
    image: ghcr.io/tamnd/githome:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.githome.rule=Host(`git.example.com`)"
      - "traefik.http.routers.githome.entrypoints=websecure"
      - "traefik.http.routers.githome.tls.certresolver=le"
      - "traefik.http.services.githome.loadbalancer.server.port=3000"
      # Redirect HTTP to HTTPS
      - "traefik.http.routers.githome-http.rule=Host(`git.example.com`)"
      - "traefik.http.routers.githome-http.entrypoints=web"
      - "traefik.http.routers.githome-http.middlewares=https-redirect"
      - "traefik.http.middlewares.https-redirect.redirectscheme.scheme=https"
    environment:
      GITHOME_HTML_BASE_URL: "https://git.example.com"
      # ... other vars

volumes:
  letsencrypt:
```

Traefik watches the Docker socket for label changes and updates routing dynamically.

## Common issues

**OAuth redirects fail or return the wrong URL.** `GITHOME_HTML_BASE_URL` is the source of truth for all generated URLs. If your proxy adds or strips a port, or you access the server via a different hostname than the one in `GITHOME_HTML_BASE_URL`, OAuth and clone URLs break. Set `GITHOME_HTML_BASE_URL` to exactly what appears in the browser address bar.

**Large uploads hang or fail with 413.** Set `client_max_body_size` in nginx to at least 512MB and add the `/api/uploads/` location block with `proxy_request_buffering off`. For Caddy, set `request_body max_size 512MB`.

**Git push times out mid-transfer.** Increase `proxy_read_timeout` in nginx (300s or more). Git push can take a long time for large repositories.

## Git LFS

Git LFS is not supported. Do not configure an LFS endpoint against a githome instance.
