# ADR 0006: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26).

## Context

checkout logged JSON with `log/slog` and nothing else: no trace or span id on a log
line, no request id, no request log, no metrics, no traces and no `traceparent` on the
events it publishes. The warehouse-systems services (inventory-storage,
wes-work-planning) already share one pipeline and one convention, recorded in their
ADRs "standard metrics convention" (inventory-storage ADR 0016, wes-work-planning
ADR 0013); customer-ordering adopted it in its ADR 0007, and this ADR replicates that
shape here:

- pods export OTLP/gRPC to the in-cluster Collector
  (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger (traces)
  and a Prometheus exporter (metrics);
- logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring a checkout confirm could not be followed from the REST call
through the sibling calls and the `CheckoutCompleted` event, a log line could not be
joined to its trace, and no dashboard could show confirm rate, errors or latency.

## Decision

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup` (OTLP/gRPC
   traces and metrics, W3C TraceContext + Baggage propagator, Go runtime metrics;
   exporters dial lazily, so no Collector means dropped telemetry, never a blocked boot
   or request; returns a shutdown func) and `WithTraceContext` (stamps
   `trace_id`/`span_id` on a log record written with a span-carrying context). The files
   are byte-identical to customer-ordering's apart from the module path. Only the
   composition roots (`cmd/api`, `cmd/mcp`) and other adapters import it;
   `internal/domain` and `internal/application` never import OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`,
   `LOG_LEVEL`, uppercase levels. Code on a request path uses the `*Context` slog
   variants (the MCP error mapper now takes the request context).
3. **HTTP middleware order** on both chi routers (REST and MCP): `RequestID` ->
   `otelchi.Middleware(serviceName, WithChiRoutes(r))` ->
   `otelchimetric.NewServerRequestDuration` -> `RequestLogger` -> `Recoverer`; the
   existing `rejectNUL` and Idempotency-Key middleware stay after them.
   `telemetry.Setup` runs before the router is built and is flushed last on shutdown.
   The request line carries `method`, `route` (the chi pattern, never the raw path),
   `status`, `duration_ms`, `bytes` and `request_id`; 5xx is ERROR, 4xx WARN, the rest
   INFO; `/healthz` and `/readyz` are not logged. `RequestLogger` lives in
   `internal/adapters/inbound/requestlog`, not in the REST adapter, because `cmd/mcp`
   may not import the REST adapter and both routers must log identically.
4. **Traces over Kafka.** `OutboxRepo.Append` stamps `traceparent`/`tracestate` from the
   active span into the stored outbox headers, inside the transaction that writes the
   aggregate (`internal/adapters/outbound/postgres/outbox_trace.go`). The relay already
   publishes stored headers unchanged as Kafka headers. Only extra headers are added:
   CloudEvents attributes and body are untouched (ADR 0004).
5. **Environment contract.** `cmd/api` and `cmd/mcp` read `OTEL_SERVICE_NAME` (defaults
   `checkout` and `checkout-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT` (default
   `telemetry.DefaultEndpoint`), `SERVICE_VERSION`, `ENVIRONMENT` (read by `Setup`) and
   `LOG_LEVEL`.
6. **Tier 2 business metric.** One counter, `checkout.sessions.confirmed`, unit
   `{session}`, attribute `outcome` (`completed` | `declined` | `requote-required` |
   `failed` | `in-doubt` | `refused`) and, for every outcome but the two successes
   (`completed`, `requote-required`), `reason`: `payment-declined`,
   `authorization-mismatch`, `placement-in-doubt`, `authorization-unknown`, the
   session's failure code (`place-order-rejected`, ...), or for a refusal
   `price-quote-expired`, `session-closed`, `not-found`, `concurrent-update`,
   `illegal-transition`, `validation-failed`, `internal-error`.
   `ports.SessionMetrics` is the application port; `ConfirmSession.Execute` calls it
   once per attempt with the outcome the shopper was answered with (the policies that
   re-drive the saga do not count, so one shopper action is one data point); the OTel
   implementation is in the telemetry adapter and is injected from `cmd/api`; a nil
   `Deps.Metrics` is a no-op.

### Why this counter

Reading `apis/openapi.yaml` and ADR 0001, the primary command is the confirm route:
open only quotes, while confirm is the saga that moves money and places the order.
Its outcomes are exactly the failure modes an operator must tell apart: payment refused
(declined), offers changed (requote-required), the pivot rejected (failed), and the
unknown outcome that locks the session (in-doubt, which the 20 h alert and 22 h
give-up depend on). Rising `declined` means payment is refusing shoppers; rising
`requote-required` means offers expire or change before shoppers confirm; rising
`in-doubt` means the place-order step is slow or failing; `refused` with
`internal-error` means the service itself is failing. One counter with an `outcome`
attribute follows the convention.

## Assumptions

- No Helm chart exists in this repository (ecommerce repos have none yet), so the chart
  part of the standard (`otel.*` values, deployment env, `render_test.sh`) is skipped, not
  invented.
- checkout consumes no topic (`cmd/api` comment, ADR 0004), so there is no Kafka
  consumer to extract `traceparent` in; the stamping side is implemented and the
  consumers of `CheckoutCompleted` (shopping-cart, customer-payments) extract it.
- `docs/adr` has no index file and `docs/runtime.md` does not exist, so neither is edited.
- A confirm that finds the session already moved on by a policy returns the current
  state and counts by that state.
- Dashboards and alert rules live in warehouse-infra and are generated per deployed
  context from the instrument name above; they are not part of this change.

## Consequences

- A request, its log lines and the event it enqueued share one trace id; `trace_id` in
  Loki jumps to Jaeger.
- Request rate, errors and duration come from `http.server.request.duration`
  (otelchimetric); confirm health from `checkout.sessions.confirmed`.
- Every new log call on a request path must use a `*Context` variant, or its line
  carries no trace id.
- `OutboxRepo.Insert` (the raw path used by tests) stamps nothing; only `Append`, the
  path every use case takes, does.
