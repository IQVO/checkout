---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: Every metric, span, log field and alert code Checkout emits, the exporter variables, and the dashboards and alerts that exist for it in warehouse-infra.
---

# Observability

Logs, metrics and traces follow the fleet observability standard
([ADR 0006](/docs/adr/0006-fleet-observability-standard)): JSON logs on stdout
(shipped by the cluster's log agent to Loki), OTLP/gRPC metrics and traces to
the Collector (Prometheus and Jaeger behind it). Export never blocks: the gRPC
exporters connect lazily and no blocking dial is set, so a Collector that is
absent or down costs telemetry and nothing else.

All three binaries (`cmd/api`, `cmd/mcp`, `cmd/a2a`) call the same
`telemetry.Setup` (`internal/adapters/outbound/telemetry/telemetry.go`) before
building their HTTP router and flush it last on shutdown (bounded to 5 s).

## Exporter and resource variables

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint. A bare `host:port` is turned into `http://host:port`. In the chart: `otel.endpoint`, default `otel-collector.observability.svc.cluster.local:4317`. |
| `OTEL_SERVICE_NAME` | `checkout` (`checkout-mcp`, `checkout-a2a`) | `service.name` on every span and metric; the chart derives `<name>-mcp` and `<name>-a2a`. |
| `SERVICE_VERSION` | `dev` | `service.version`. The chart sets the image tag. |
| `ENVIRONMENT` | `local` | `deployment.environment.name`. The chart sets `production`. |
| `OTEL_RESOURCE_ATTRIBUTES` | none | merged by the SDK's default resource. |
| `LOG_LEVEL` | `info` | log level (the OpenTelemetry SDK's own diagnostics are routed through the same logger; exporter errors are logged at DEBUG). |

Metrics are pushed every 15 seconds. The propagator is W3C `traceparent` plus
baggage.

## Metrics

| Instrument | Type | Unit | Attributes | Emitted by |
| --- | --- | --- | --- | --- |
| `checkout.sessions.confirmed` | counter (int64) | `{session}` | `outcome`, `reason` (set only when there is one) | `cmd/api`, one increment per ConfirmSession attempt (`internal/adapters/outbound/telemetry/session_metrics.go`, meter `github.com/IQVO/checkout`) |
| `http.server.request.duration` | histogram (float64) | `s` | HTTP method, route pattern and status, as set by otelchi's metric middleware | all three binaries (`otelchimetric`); buckets 5 ms to 10 s. Prometheus exposes it as `http_server_request_duration_seconds_*`. |
| Go runtime (`go.*` / `runtime.go.*`) | gauges and counters | various | none | OTel Go runtime instrumentation (goroutines, heap, GC) |

`outcome` is one of `completed`, `declined`, `requote-required`, `failed`,
`in-doubt` or `refused`. `reason` says why when the outcome is not a success:

| `outcome` | Meaning | Typical `reason` |
| --- | --- | --- |
| `completed` | the order was placed | none |
| `requote-required` | the network re-quoted; the authorization is voided | none |
| `declined` | payment refused, or the authorized amount was not the quote total | `payment-declined`, `authorization-mismatch`, or `authorization-rejected: <slug>` |
| `failed` | the place-order step rejected the request (or the session had already failed) | the failure code, `place-order-rejected` |
| `in-doubt` | the outcome is unknown and a policy resolves it | `placement-in-doubt` (PLACING) or `authorization-unknown` (AUTHORIZING) |
| `refused` | the confirm was refused before or after the saga | `session-closed`, `price-quote-expired`, `not-found`, `concurrent-update`, `illegal-transition`, `validation-failed`, `internal-error` |

Reading it (from the instrument's own description): a falling `completed`
share with rising `declined` means payment is refusing shoppers; rising
`requote-required` means offers expire or change before shoppers confirm;
rising `in-doubt` means the place-order step is slow or failing and orders may
be unaccounted for; rising `refused` with `internal-error` means the service
itself is failing.

There is no gauge for outbox depth or session-state counts: use the SQL in the
[Runbook](/docs/operations/runbook#outbox-relay).

## Traces

- **HTTP server span** per request on every binary, from `otelchi`, named
  after the chi route pattern (`POST /checkout-sessions`, not the session id).
  The request logger runs inside the span, so its record carries `trace_id`
  and `span_id`.
- **`a2a.skill <skill>`** (`internal/adapters/inbound/a2a/skills.go`): one
  span per skill call in `cmd/a2a`, attribute `a2a.skill`; status Error with
  the problem detail when the skill answers a problem.
- The outbound HTTP calls to the five siblings use a plain `net/http` client
  without OpenTelemetry instrumentation, so they are not child spans and the
  siblings do not receive a `traceparent`.
- **Trace to Kafka.** `traceparent` and `tracestate` of the active span are
  stamped into the outbox row's headers inside the same transaction
  (`internal/adapters/outbound/postgres/outbox_trace.go`), and the relay
  publishes them as ordinary Kafka headers; a consumer can parent its span on
  the request that caused the event.
- The MCP adapter has no span of its own beyond the HTTP server span.

## Logs

JSON, one object per line on stdout, written by `log/slog`. Records written
with a span-carrying context also carry `trace_id` and `span_id`.

### Request line

One line per HTTP request on every binary (`/healthz` and `/readyz` excluded):

```json
{"time":"2026-10-31T11:28:59.066321-03:00","level":"INFO","msg":"http request","method":"POST","route":"/checkout-sessions/{sessionId}:confirm","status":200,"duration_ms":22,"bytes":530,"request_id":"host/6PjAIOZFQZ-000005","trace_id":"766e93aad4b6a5d1c52e413bbd667a19","span_id":"f621fd37e5f0d148"}
```

Fields: `method`, `route` (the matched pattern, `unmatched` when nothing
matched, never the raw path), `status`, `duration_ms`, `bytes`, `request_id`.
Level: 5xx is ERROR, 4xx WARN, everything else INFO.

### Messages

| Level | `msg` | Where | Fields |
| --- | --- | --- | --- |
| INFO | `telemetry configured` | all | `service_name`, `otlp_endpoint` |
| INFO | `postgres adapters configured` / `postgres repositories configured` | api / mcp, a2a | |
| INFO | `succeeded after retry` | boot | `op`, `attempt` |
| WARN | `retrying` | boot (migrations, Postgres ping) | `op`, `attempt`, `in`, `err` |
| INFO | `scheduler running` | api | `job`, `interval` |
| INFO | `outbox relay running` | api | `interval`, `max_attempts`, `events_topic`, `analytics_topic` |
| INFO | `http server listening` / `mcp server listening (Streamable HTTP)` / `a2a agent listening (JSON-RPC over HTTP)` | all | `addr` |
| INFO | `scheduled sweep done` | api, only when a tick handled something | `job`, `handled` |
| ERROR | `scheduled sweep failed; trying again at the next tick` | api | `job`, `handled`, `error` |
| ERROR | `outbox relay pass failed` | api | `error` |
| ERROR | `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | api | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` |
| WARN | `readiness: dependency is not ready` | api | `dependency` (`postgres` or `kafka`), `error` |
| WARN | `authorization outcome unknown, the session stays AUTHORIZING` | api | `sessionId`, `error` |
| WARN | `void failed, it stays owed` / `recording the void failed` | api | `sessionId`, `error` |
| WARN | `alert` | api | `code`, `sessionId`, `detail` (companion of every ERROR `ALERT`) |
| WARN | `requoted offer time zone unusable; delivery dates fall back to UTC` | api | the zone and the offer |
| WARN | `invalid <VAR>; using the default` / `ignoring invalid SHUTDOWN_DRAIN_DELAY` | api | `value`, `default` |
| ERROR | `request failed` | api | `error`, `method`, `path` (an unmapped error answered 500) |
| ERROR | `mcp tool failed with an unexpected error` / `a2a skill failed with an unexpected error` | mcp, a2a | `error` |
| INFO | `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | api | `drain_delay` |
| WARN | `worker did not stop before the shutdown drain deadline` | api | `worker` |
| ERROR | `service exited with error` / `mcp server exited with error` / `a2a agent exited with error` | all | `error` |

### Alerts

The saga raises named alerts as ERROR lines through `internal/adapters/outbound/alerts`:

```json
{"level":"ERROR","msg":"ALERT","alert":"placement-in-doubt-20h","sessionId":"sess-...","detail":"in doubt for 20h0m0s"}
```

| `alert` | Raised when |
| --- | --- |
| `placement-in-doubt-20h` | a session has been `PLACING` for 20 h (once) |
| `placement-given-up-22h` | the 22 h give-up fired: `FAILED` with the void owed |
| `place-order-rejected` | customer-ordering answered 400 or 422 (a contract bug on this side) |
| `authorization-mismatch` | customer-payments authorized an amount or currency that is not the quote total |
| `sibling-contract-violation` | sibling data cannot form a consistent session (HTTP 502 `upstream-contract-violation`) |

No rule in warehouse-infra matches these `alert` codes today (see below); they
are visible in Loki.

## Dashboards and alerts that exist

Only what is in warehouse-infra `develop`:

- **Dashboard** `terraform/dashboards/contexts/checkout.json`, "Warehouse —
  Checkout" (`http://localhost:3000/d/warehouse-checkout` on the local
  cluster). Panels: Kong request rate by code, Kong 5xx error rate, Kong
  latency p95 (request vs upstream), request rate by route and request
  duration p95 per process (`http_server_request_duration_seconds_*` for
  `service_name=~"checkout.*"`), Go runtime goroutines and memory, and Loki log
  panels for `{app="checkout"}` (live stream, volume by level, errors and
  warnings). **No panel queries `checkout.sessions.confirmed`**.
- **Alert rules** `terraform/alerting/warehouse-alert-rules.json`, groups
  `warehouse-checkout` and `warehouse-checkout-slo`:
  `WarehouseKong5xxRatioHigh` (5xx ratio above 5 %, per Kong route),
  `WarehousePodRestarting` (two or more restarts of `checkout-.+` in 15 min),
  and the SLO burn-rate rules `WarehouseApiAvailabilityBurnFast`,
  `WarehouseApiLatencyBurnFast`, `WarehouseApiAvailabilityBurnSlow`,
  `WarehouseApiLatencyBurnSlow` (availability objective 99 %, latency
  objective 95 % under 400 ms). All link to
  `docs/operations/runbook.md` of this repository (`runbook_url`).
- There is no outbox-lag rule for checkout (`WarehouseOutboxLagHigh` exists
  for other contexts only) and no rule on the `ALERT` log codes or on
  `checkout.sessions.confirmed`.
