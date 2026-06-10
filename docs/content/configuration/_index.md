---
title: "Configuration"
description: "how to configure githome using environment variables and env files"
weight: 5
---

Githome is configured entirely through environment variables. There are no YAML or TOML config files to maintain. Every setting has a `GITHOME_` prefix.

This section covers the configuration options in four areas:

- [Overview](overview/) - precedence rules, env file format, secrets auto-generation, and startup validation
- [Database](database/) - SQLite and PostgreSQL connection options, migrations, and the git data directory
- [Networking](networking/) - bind address, public URL, TLS termination, timeouts, and blob size limits
- [Observability](observability/) - log levels, structured log fields, health check endpoints, and debug mode
