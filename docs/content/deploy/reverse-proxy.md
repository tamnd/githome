---
title: "Reverse proxy"
description: "terminate TLS in front of githome with Caddy, nginx, or Traefik"
weight: 20
---

githome does not terminate TLS itself. Put a reverse proxy in front of it and
set `GITHOME_HTML_BASE_URL` to the public HTTPS URL. That value is used in
OAuth redirects, webhook payloads, and git clone URLs, so it must match what
clients type in their browser.

## Caddy (simplest)

Caddy provisions and renews Let's Encrypt certificates automatically:

```caddyfile
git.example.com {
    reverse_proxy localhost:3000
}
```

## nginx

```nginx
server {
    listen 443 ssl http2;
    server_name git.example.com;

    ssl_certificate     /etc/letsencrypt/live/git.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/git.example.com/privkey.pem;

    # large enough for release asset uploads
    client_max_body_size 512m;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## Traefik (Docker labels)

```yaml
services:
  githome:
    image: ghcr.io/tamnd/githome:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.githome.rule=Host(`git.example.com`)"
      - "traefik.http.routers.githome.entrypoints=websecure"
      - "traefik.http.routers.githome.tls.certresolver=letsencrypt"
      - "traefik.http.services.githome.loadbalancer.server.port=3000"
```
