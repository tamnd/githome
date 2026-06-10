---
title: "Observability"
description: "log levels, structured log fields, health check endpoints, and debug mode"
weight: 40
---

## Log level

```ini
GITHOME_LOG_LEVEL=info
```

Valid values: `debug`, `info`, `warn`, `error`. The default is `info`.

`info` logs one line per HTTP request and one line per significant background operation (migration applied, webhook delivered, repository created). `warn` and `error` log only problems. `debug` logs everything described in the [debug mode](#debug-mode) section below.

## Log format

```ini
GITHOME_LOG_FORMAT=json
```

Valid values: `text`, `json`. The default is `text`.

`text` format produces human-readable output useful during development:

```
2026-06-10T12:34:56Z INFO  request method=GET path=/api/v1/repos/alice/myrepo status=200 duration=3ms
```

`json` format produces one JSON object per line, suitable for log aggregation pipelines:

```json
{"time":"2026-06-10T12:34:56Z","level":"INFO","msg":"request","service":"githome","version":"0.1.2","method":"GET","path":"/api/v1/repos/alice/myrepo","status":200,"duration_ms":3}
```

Use `json` in production. It integrates directly with Loki, Datadog, Elastic, and most other log aggregators without a parsing step.

## Structured log fields

Every log line includes these fields:

| Field | Type | Description |
|-------|------|-------------|
| `service` | string | Always `githome` |
| `version` | string | Binary version, e.g. `0.1.2` |
| `level` | string | `DEBUG`, `INFO`, `WARN`, or `ERROR` |
| `msg` | string | Short event description |

Request log lines add:

| Field | Type | Description |
|-------|------|-------------|
| `method` | string | HTTP method |
| `path` | string | Request path, without query string |
| `status` | int | HTTP response status code |
| `duration_ms` | int | Wall time to write the full response |
| `user` | string | Authenticated username, omitted for anonymous requests |

Error log lines add:

| Field | Type | Description |
|-------|------|-------------|
| `err` | string | Error message |

Example Loki query to find all 5xx responses in the last hour:

```logql
{service="githome"} | json | status >= 500
```

## Health check endpoints

```sh
curl -s http://localhost:3000/healthz
# {"status":"ok"}

curl -s http://localhost:3000/readyz
# {"status":"ok"}
```

Both endpoints return `HTTP 200` with `{"status":"ok"}` when the server is running and the database connection pool is healthy. They return `HTTP 503` if the database is unreachable.

`/healthz` is a liveness probe: it checks that the process is running. `/readyz` is a readiness probe: it checks that the process can serve traffic, including a round-trip ping to the database.

In Kubernetes:

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 3000
  initialDelaySeconds: 5
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 3000
  initialDelaySeconds: 5
  periodSeconds: 10
```

Neither endpoint requires authentication.

## Debug mode

```ini
GITHOME_LOG_LEVEL=debug
```

At `debug` level, githome logs:

- Every SQL query with bind parameters and execution time
- Every git command invocation with arguments
- Webhook delivery attempts and response bodies
- OAuth token exchange steps
- Template render times (when web UI is enabled)

This produces significant log volume. Use it during development or when diagnosing a specific problem, not in steady-state production.

Example debug output for a single API request:

```
2026-06-10T12:34:56Z DEBUG sql query="SELECT * FROM repositories WHERE owner_id = $1 AND name = $2" args=[42,"myrepo"] duration_ms=1
2026-06-10T12:34:56Z DEBUG git cmd="git" args=["--git-dir","/var/lib/githome/repos/42","log","--format=%H","-1"] duration_ms=8
2026-06-10T12:34:56Z INFO  request method=GET path=/repos/alice/myrepo status=200 duration_ms=12
```

## Log aggregation

Githome does not expose Prometheus metrics or OpenTelemetry traces. Use structured log aggregation to build dashboards and alerts:

- **Loki + Grafana**: ship `json` logs via Promtail or the Docker log driver, then query with LogQL
- **Datadog**: use the Datadog Agent log collector pointing at `stdout`; autodiscovery parses `json` format automatically
- **Elastic**: use Filebeat or Fluentd to forward `json` logs to Elasticsearch, then build dashboards in Kibana

Key metrics to derive from logs:

- Request rate: count `msg="request"` events per minute
- Error rate: count `status >= 500` per minute
- p99 latency: percentile over `duration_ms` where `msg="request"`
- Slow git operations: filter `msg` contains `"git"` and `duration_ms > 1000`
