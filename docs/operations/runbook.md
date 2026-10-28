# Checkout runbook

Checkout is the retail network's storefront purchase saga: it opens a checkout session from a shopper's cart, authorizes the payment with customer-payments, places the customer order with customer-ordering as the pivot and confirms the delivery promise, with compensating voids and five clock-driven sweep policies cleaning up the sessions that stall on the way (`README.md`, `docs/adr/0001`, `docs/adr/0003`). It deploys as two binaries from one image: the `api` (always on) and the read-only MCP server (optional, ADR 0005). This runbook covers both and what to do when they misbehave.

## Runtime shape (two Deployments from one image)

The `api` Deployment (`charts/checkout/templates/deployment.yaml`) runs `cmd/api` — the composition root that wires, in one binary and one pod:

- **REST API** on `HTTP_ADDR` (default `:8080`): `POST /checkout-sessions`, `GET /checkout-sessions/{sessionId}`, `POST /checkout-sessions/{sessionId}:confirm` (`internal/adapters/inbound/http/server.go`). Both POSTs require an `Idempotency-Key` header.
- **Transactional outbox relay**: polls the `outbox_events` table every `OUTBOX_RELAY_INTERVAL` and publishes pending rows to Kafka (`internal/adapters/outbound/outbox`).
- **Five clock-driven saga policies**, each on its own tick: ResolveInDoubtPlacements (`INDOUBT_SWEEP_INTERVAL`, default 15s), ResolveStuckAuthorizations (`STUCK_SWEEP_INTERVAL`, 30s), RetryOwedVoids (`VOID_SWEEP_INTERVAL`, 1m), ExpireOnQuoteExpiry (`EXPIRY_SWEEP_INTERVAL`, 1m) and PurgeStoredPlacements (`PURGE_SWEEP_INTERVAL`, 1h).

There is no in-memory mode and no worker that can be switched off. If the relay or a policy returns on its own, the process exits non-zero so the orchestrator replaces the pod (`cmd/api/main.go`).

The optional `mcp` Deployment (`charts/checkout/templates/mcp-deployment.yaml`, default off) runs `cmd/mcp` (ADR 0005): a **read-only** MCP server serving the one tool `get_checkout_session` over the SAME `GetSession` use case the REST adapter uses, over Streamable HTTP `/mcp` on `MCP_ADDR` (default `:8090`) with an open, unauthenticated `/healthz`. It shares the service database (the same Secret), runs the idempotent embedded migrations at boot like `cmd/api`, and never starts the relay or a policy, never dials Kafka and never calls a sibling. There is NO auth of any kind (fleet-wide decision, ADR 0005): access control is the in-cluster ClusterIP boundary plus whatever the gateway allows.

Graceful shutdown of the api is sequential (`cmd/api/main.go`, `cmd/api/workers.go`): `/readyz` flips to 503 → wait `SHUTDOWN_DRAIN_DELAY` (default 5s) → drain HTTP (10s bound) → stop the five schedulers one after the other (10s each, 50s) → stop the relay (10s) → close the pool. Worst case 75s; the chart refuses a `terminationGracePeriodSeconds` that does not exceed drain + 70s (default 80, `charts/checkout/values.yaml`).

## Dependencies

| Dependency | Used for | Source |
| --- | --- | --- |
| Postgres (via PgBouncer) | every write and read of both binaries; `DATABASE_URL` is the pooled DSN | Secret (`database.existingSecret`), key `DATABASE_URL` |
| Postgres (direct DSN) | boot migrations only; `MIGRATIONS_DATABASE_URL` | same Secret, key `MIGRATIONS_DATABASE_URL`, optional — falls back to `DATABASE_URL` |
| Kafka (`KAFKA_BROKERS`) | outbox relay sink, `/readyz` broker check | chart value `kafka.brokers` |
| shopping-cart (REST) | the saga reads the cart (`config.shoppingCartUrl`) | in-cluster sibling base URL |
| delivery-promise (REST) | quote / confirm the delivery promise (`config.deliveryPromiseUrl`) | in-cluster sibling base URL |
| pricing (REST) | price the offer (`config.pricingUrl`) | in-cluster sibling base URL |
| customer-payments (REST) | authorize / void the payment (`config.customerPaymentsUrl`) | in-cluster sibling base URL |
| customer-ordering (REST) | place the order — the saga pivot (`config.customerOrderingUrl`) | in-cluster sibling base URL |
| OTEL Collector | OTLP/gRPC traces + metrics (ADR 0006) | `otel.endpoint` (default `otel-collector.observability.svc.cluster.local:4317`) |

Notes:

- All five sibling URLs are MANDATORY: `cmd/api` exits at boot naming the first missing one (`cmd/api/config.go`); the chart refuses to render without them.
- Migrations are embedded and run at boot inside a bounded retry (`internal/bootretry`); exhaustion refuses boot.
- Produced topics: `retailnetwork.checkout.events` and `retailnetwork.checkout.analytics` (`internal/adapters/outbound/kafka/encoder.go`). This context CONSUMES no topic: no consumer, no consumer group.
- The Collector is never on the request path: exporters dial lazily, so a Collector that is down costs telemetry and nothing else (ADR 0006).

## Health

- `GET /healthz` — liveness only, on both binaries (the mcp server serves it open on :8090). Never flipped by dependencies or by shutdown. The chart's startup and liveness probes use it.
- `GET /readyz` (api only) — readiness. 200 only while (a) the pod is not draining AND (b) a Postgres ping AND a Kafka broker metadata request both answer; the verdict is cached 3s and one check round is bounded 2s (`cmd/api/main.go`, `internal/adapters/inbound/http/readiness.go`). Otherwise 503 naming the failing dependency.
- Kafka down → `/readyz` 503 but REST writes are still accepted; the event waits in `outbox_events`. Postgres down → `/readyz` 503 and REST writes fail. In both cases `/healthz` stays 200, so probes do not restart the pod.

## Configuration

Env vars `cmd/api` reads (contract: `cmd/api/main.go` header comment; rendered in `charts/checkout/templates/deployment.yaml` and the ConfigMap):

| Env var | Chart value | Default | Notes |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `config.httpAddr` | `:8080` | port must equal `service.targetPort` |
| `LOG_LEVEL` | `config.logLevel` | `info` | `debug`, `info`, `warn`/`warning` or `error`; JSON logs on stdout |
| `DATABASE_URL` | Secret key `database.existingSecretKey` | required | pooled DSN (PgBouncer); never in the ConfigMap |
| `MIGRATIONS_DATABASE_URL` | Secret key `database.migrationsExistingSecretKey` | `DATABASE_URL` | direct (session-mode) DSN, boot migrations only |
| `KAFKA_BROKERS` | `kafka.brokers` | required | comma-separated broker list |
| `SHOPPING_CART_URL` | `config.shoppingCartUrl` | required | base URL of shopping-cart |
| `DELIVERY_PROMISE_URL` | `config.deliveryPromiseUrl` | required | base URL of delivery-promise |
| `PRICING_URL` | `config.pricingUrl` | required | base URL of pricing |
| `CUSTOMER_PAYMENTS_URL` | `config.customerPaymentsUrl` | required | base URL of customer-payments |
| `CUSTOMER_ORDERING_URL` | `config.customerOrderingUrl` | required | base URL of customer-ordering |
| `SIBLING_TIMEOUT` | `config.siblingTimeout` | `5s` | per-call timeout of the siblings; empty = not rendered |
| `CUSTOMER_ORDERING_TIMEOUT` | `config.customerOrderingTimeout` | `15s` | bounds the place-order pivot call; empty = not rendered |
| `INDOUBT_SWEEP_INTERVAL` | `config.indoubtSweepInterval` | `15s` | ResolveInDoubtPlacements tick |
| `STUCK_SWEEP_INTERVAL` | `config.stuckSweepInterval` | `30s` | ResolveStuckAuthorizations tick |
| `VOID_SWEEP_INTERVAL` | `config.voidSweepInterval` | `1m` | RetryOwedVoids tick |
| `EXPIRY_SWEEP_INTERVAL` | `config.expirySweepInterval` | `1m` | ExpireOnQuoteExpiry tick |
| `PURGE_SWEEP_INTERVAL` | `config.purgeSweepInterval` | `1h` | PurgeStoredPlacements tick |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` | `1s` | Go duration or plain seconds |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` | `20` | failures before the relay excludes a row; `0` = unlimited |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` | `5s` | whole seconds/minutes; sizes the grace period |
| `ENVIRONMENT` | `environment` | `production` | `deployment.environment.name` on every span, metric and log |
| `SERVICE_VERSION` | `image.tag` (else appVersion) | `dev` | `service.version` |
| `OTEL_SERVICE_NAME` | `otel.serviceName` | `checkout` | rendered only with `otel.enabled=true` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel.endpoint` | Collector address | rendered only with `otel.enabled=true` |

The mcp component reads only: `MCP_ADDR` (`mcp.httpAddr`), `LOG_LEVEL`, `DATABASE_URL` / `MIGRATIONS_DATABASE_URL` (the same Secret as the api), `SERVICE_VERSION` (`mcp.serviceVersion`, else the image tag), `ENVIRONMENT` (`mcp.environment`), `OTEL_SERVICE_NAME` (`<otel.serviceName>-mcp`) and `OTEL_EXPORTER_OTLP_ENDPOINT`. Anything else (Kafka, siblings, schedulers, relay) is never rendered into it — the selector test fails the chart if it is.

Behavior — Idempotency-Key middleware (`internal/adapters/inbound/http/idempotency.go`):

- Both POST routes (`/checkout-sessions`, `/checkout-sessions/{sessionId}:confirm`) require an `Idempotency-Key` header of 1–255 characters.
- The first response below 500 is stored per `(route, key)` for 24h (`internal/application/idempotency` `TTL`).
- A repeat with the same request replays the stored response with header `Idempotent-Replayed: true`; the same key with a different request is 422 `idempotency-key-reused`. 5xx responses are never stored, so the key stays retryable.

## Alerts and dashboards

- Traces in Jaeger (`service.name` = `checkout` / `checkout-mcp`); metrics in Prometheus via the Collector's exporter; logs in Loki (stdout JSON, `trace_id`/`span_id` stamped on request-path records, ADR 0006).
- Business metric: the counter **`checkout.sessions.confirmed`** records the outcome of every ConfirmSession attempt (`ports.SessionMetrics`, `internal/adapters/outbound/telemetry/session_metrics.go`) — the confirm rate and its failure split come from it.
- Every HTTP request of both routers is logged one line with `method`, `route` (the chi pattern), `status`, `duration_ms`, `bytes` and `request_id`; 5xx is ERROR, 4xx WARN. `/healthz` and `/readyz` are not logged.
- `traceparent`/`tracestate` from the active span are stamped into the outbox row headers, so a published event joins the trace of the REST call that caused it.

## Failure modes

### Pod crash-loop

- Symptom: restarts climb, the pod-restart alert fires.
- Diagnosis: `kubectl -n warehouse-systems logs deploy/checkout --previous` — the process logs `service exited with error` naming the missing env var (`cmd/api/config.go`), migrations/ping exhaustion (`internal/bootretry`), or a stopped worker. The mcp pod logs `mcp server exited with error`.
- Action: fix the named chart value or Secret key, then roll again.

### Postgres unreachable

- Symptom: `/readyz` 503 naming `postgres`; REST writes fail; the mcp server cannot serve sessions (every read is a Postgres query).
- Diagnosis: check the Secret's `DATABASE_URL` (pooled) and PgBouncer; the readiness verdict refreshes within 3s of recovery.
- Action: restore Postgres/PgBouncer — the relay and the policies resume on their own and nothing needs replaying; the pod is not killed (`/healthz` stays 200).

### Kafka unreachable

- Symptom: `/readyz` 503 naming `kafka`; `/healthz` 200; REST writes still accepted and queued.
- Diagnosis: `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;` grows while brokers are down; the relay retries every `OUTBOX_RELAY_INTERVAL`.
- Action: restore the brokers; the relay drains in `id` order automatically.

### A sibling is down (shopping-cart, delivery-promise, pricing, customer-payments, customer-ordering)

- Symptom: 503 `<sibling>-unavailable` from the POST routes (e.g. `shopping-cart-unavailable`, `delivery-promise-unavailable`); the saga state machine leaves the session in a waiting state the sweep policies pick up.
- Diagnosis: the request log line names the route and status; the trace shows which sibling call failed (the sibling spans carry the same `trace_id`).
- Action: restore the sibling; the in-doubt/stuck sweeps resolve the half-finished sessions on their own — nothing to replay by hand.

### Outbox relay stuck / row parked after max attempts

- Symptom: repeating relay-pass ERRORs, or the one-time ERROR that a row reached the attempt cap and is excluded from future claims.
- Diagnosis: `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id;` — a row whose `attempts` reached `OUTBOX_MAX_ATTEMPTS` is excluded and later rows overtake it.
- Action: fix the cause (usually Kafka), then re-arm the row: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`.

### idempotency-key-reused storms

- Symptom: a burst of 422 `idempotency-key-reused` from the POST routes.
- Diagnosis: the middleware hashes method+path+query+body, so a storm means callers reuse a key while regenerating the body; correct replays come back with `Idempotent-Replayed: true` and are not a fault.
- Action: fix the caller to keep the body byte-stable per logical request or mint a fresh key; records age out after 24h — nothing to clear server-side.

## Operational actions

- Restart: `kubectl -n warehouse-systems rollout restart deploy/checkout` (and `deploy/checkout-mcp` when the mcp component is enabled).
- Logs: Loki, `app=checkout` (JSON slog on stdout; raise `config.logLevel` to `debug` for a noisy pod).
- In-cluster REST: Service port 80 → container 8080; the external path is the gateway route `/api/checkout` (prefix stripped, `gatewayApi.*`).
- MCP endpoint: with `mcp.enabled=true` and `mcp.route.enabled=true`, AI clients reach the read-only server at the gateway path **`/api/mcp/checkout`**, prefix rewritten to `/mcp` (the SDK's canonical endpoint) on Service `checkout-mcp:8090`. In-cluster without the route: `http://checkout-mcp:8090/mcp`; health at `/healthz`. NO auth of any kind — the ClusterIP boundary is the access control (ADR 0005).
- Scale: HPA off by default (`autoscaling.api`); N>1 is safe for the relay (row claims) and the saga writes (optimistic state transitions), but has not been load-tested (`charts/checkout/values.yaml`).
