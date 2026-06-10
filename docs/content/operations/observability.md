---
title: "Observability"
description: "health endpoints, structured logs, log shipping, and alerting patterns for githome"
weight: 40
---

## Health endpoints

Githome exposes two HTTP health endpoints that require no authentication.

```
GET /readyz   200 = instance is ready to serve traffic
              503 = not ready (startup incomplete, database unreachable)

GET /healthz  200 = process is alive
```

Use `/readyz` for load balancer health checks and container `healthcheck` directives. Use `/healthz` for simple liveness probes where you only need to know the process is running.

```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/readyz
# 200
```

A 503 from `/readyz` means githome is alive but cannot serve traffic. Check `GITHOME_DATABASE_URL` connectivity and logs.

## Log format

Githome logs via Go's `slog` package. Every log line includes a standard set of fields.

Common fields across all log lines:

| Field       | Type   | Description                              |
|-------------|--------|------------------------------------------|
| `time`      | string | RFC3339 timestamp                        |
| `level`     | string | debug, info, warn, error                 |
| `msg`       | string | human-readable event description         |
| `service`   | string | always `githome`                         |
| `version`   | string | binary version                           |

HTTP request fields (access log lines):

| Field        | Type    | Description                    |
|--------------|---------|--------------------------------|
| `method`     | string  | HTTP method                    |
| `path`       | string  | request path                   |
| `status`     | int     | HTTP response status code      |
| `duration`   | string  | request duration, e.g. `12ms`  |
| `ip`         | string  | client IP (respects X-Real-IP) |

Error fields:

| Field  | Type   | Description       |
|--------|--------|-------------------|
| `err`  | string | error message     |

## Log levels

Set the level with `GITHOME_LOG_LEVEL`.

- `debug` - all of the above, plus SQL queries, git command invocations, and authentication token validation steps. Do not use in production unless diagnosing a specific issue; it logs sensitive data.
- `info` (default) - HTTP access log, startup/shutdown events, webhook deliveries, background job completions and errors.
- `warn` - anomalies that are not fatal: retried database operations, slow queries, webhook delivery failures.
- `error` - unhandled errors, panics recovered, database connectivity failures.

## Text vs JSON format

Set `GITHOME_LOG_FORMAT=text` for human-readable output during development:

```
2026-06-10T02:00:00Z INFO  GET /repos/alice/myrepo status=200 duration=4ms
```

Set `GITHOME_LOG_FORMAT=json` in production for log aggregation:

```json
{"time":"2026-06-10T02:00:00Z","level":"INFO","msg":"request","service":"githome","version":"0.1.2","method":"GET","path":"/repos/alice/myrepo","status":200,"duration":"4ms","ip":"10.0.0.5"}
```

JSON format is the right choice any time logs flow into a collector or aggregation system.

## Shipping logs

### Vector

```toml
# /etc/vector/vector.toml

[sources.githome]
type = "journald"
include_units = ["githome.service"]

[transforms.parse_json]
type   = "remap"
inputs = ["githome"]
source = '''
  . = parse_json!(.message)
'''

[sinks.loki]
type     = "loki"
inputs   = ["parse_json"]
endpoint = "http://loki:3100"

  [sinks.loki.labels]
  service = "githome"
  level   = "{{ level }}"
```

### Fluentd

```xml
<source>
  @type systemd
  tag githome
  matches [{"_SYSTEMD_UNIT": "githome.service"}]
</source>

<filter githome>
  @type parser
  key_name message
  <parse>
    @type json
  </parse>
</filter>

<match githome>
  @type elasticsearch
  host elasticsearch
  port 9200
  logstash_format true
  logstash_prefix githome
</match>
```

### Datadog agent

Add to `/etc/datadog-agent/conf.d/githome.d/conf.yaml`:

```yaml
logs:
  - type: journald
    source: githome
    service: githome
    log_processing_rules:
      - type: multi_line
        name: new_log_start
        pattern: '^\{"time"'
```

Set `GITHOME_LOG_FORMAT=json` and restart the Datadog agent.

## Metrics

Githome does not expose a Prometheus metrics endpoint in the current release. Use log-based metrics as a substitute.

Extract request rates and latencies from access logs with Vector or a log aggregation query:

```bash
# Count 5xx responses in the last minute from journald
journalctl -u githome --since "1 minute ago" -o json \
  | jq 'select(.MESSAGE | fromjson? | .status >= 500) | .MESSAGE' \
  | wc -l
```

With Loki + LogQL:

```logql
# 5xx rate
rate({service="githome"} | json | status >= 500 [5m])

# p99 request duration (requires duration field parsed as a number)
quantile_over_time(0.99, {service="githome"} | json | unwrap duration [5m])
```

## Background jobs

Githome runs background jobs for webhook delivery and cleanup tasks. Each job logs completion and any errors at `info` or `warn` level:

```json
{"level":"INFO","msg":"webhook delivered","hook_id":42,"delivery_id":"abc123","status":200,"duration":"87ms"}
{"level":"WARN","msg":"webhook delivery failed","hook_id":42,"delivery_id":"def456","status":503,"attempt":3}
```

## Webhook delivery failures

Failed webhook deliveries are stored and visible via the API. Inspect them directly:

```bash
# List recent deliveries for a hook
curl -s \
  -H "Authorization: Bearer <token>" \
  "https://git.example.com/repos/alice/myrepo/hooks/1/deliveries" \
  | jq '.[] | {id, event, status_code, delivered_at}'
```

Redeliver a failed delivery:

```bash
curl -s -X POST \
  -H "Authorization: Bearer <token>" \
  "https://git.example.com/repos/alice/myrepo/hooks/deliveries/456/attempts"
```

## Alerting patterns

The following conditions warrant alerts in a production deployment:

**5xx rate elevated.** Any sustained rate of HTTP 5xx responses above your baseline. Query access logs grouped by `status >= 500`.

**Health check failing.** `/readyz` returning anything other than 200. This is the most important alert; it means githome is not serving traffic.

**Git push failures.** Log lines with `msg` containing `git-receive-pack` and a non-zero exit code. These indicate failed pushes, which users notice immediately.

**Webhook delivery error rate.** A stream of `webhook delivery failed` log lines, especially across multiple repositories, often indicates a misconfigured endpoint or a connectivity problem.

**Slow requests.** HTTP requests with `duration > 5s` on non-upload paths signal database or git performance problems.

**Database connectivity errors.** Any `err` field containing the string `connection refused` or `database is locked` (SQLite under high write load). These precede downtime.
