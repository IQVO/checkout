---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
description: Deploying and operating Checkout - runtime shape, Helm chart, probes, migrations, Kafka topics, outbox relay, saga sweepers, alerts and the routine procedures.
---

# Checkout runbook

Checkout is the retail network's storefront purchase saga: it opens a checkout
session from a shopper's cart, authorizes the payment with customer-payments,
places the customer order with customer-ordering as the pivot and confirms the
delivery promise, with compensating voids and five clock-driven sweep policies
cleaning up the sessions that stall on the way
([ADR 0001](/docs/adr/0001-checkout-bounded-context),
[ADR 0003](/docs/adr/0003-synchronous-contracts-and-the-anti-corruption-layer)).
It deploys as up to three binaries from one image: the `api` (always on), the
read-only MCP server (optional) and the read-only A2A agent (optional). This
runbook covers all of them and what to do when they misbehave. Settings are in
[Configuration](/docs/operations/configuration), metrics and logs in
[Observability](/docs/operations/observability), symptom lookups in
[Troubleshooting](/docs/operations/troubleshooting).

## Runtime shape

The image (`Dockerfile`) builds every `cmd/*` into `/app/<name>` and runs as
uid 1000 with a read-only root filesystem; the default `ENTRYPOINT` is `./api`
and the chart picks `command: ["/app/mcp"]` or `["/app/a2a"]` for the optional
components. `EXPOSE 8080 8090 8093`.

| Component | Binary | Port | Default in chart | What runs in it |
| --- | --- | --- | --- | --- |
| `api` | `cmd/api` | `:8080` | always on | REST, the transactional outbox relay, the five saga policies |
| `mcp` | `cmd/mcp` | `:8090` | `mcp.enabled=false` | read-only MCP server (`/mcp`, `/`), `GET /healthz` |
| `a2a` | `cmd/a2a` | `:8093` | `a2a.enabled=false` | read-only A2A agent (`/`, `/a2a`), Agent Card, `GET /healthz` |

`cmd/api` wires, in one process:

- **REST API** on `HTTP_ADDR`: `POST /checkout-sessions`,
  `GET /checkout-sessions/{sessionId}`,
  `POST /checkout-sessions/{sessionId}:confirm`, `GET /healthz`, `GET /readyz`
  (`internal/adapters/inbound/http/server.go`). Both POSTs require an
  `Idempotency-Key`.
- **Outbox relay**: polls `outbox_events` and publishes pending rows to Kafka
  (`internal/adapters/outbound/outbox`).
- **Five policies**, each on its own tick: `ResolveInDoubtPlacements` (15 s),
  `ResolveStuckAuthorizations` (30 s), `RetryOwedVoids` (1 m),
  `ExpireOnQuoteExpiry` (1 m) and `PurgeStoredPlacements` (1 h).

There is no in-memory mode and no worker that can be switched off. If the relay
or a policy returns on its own, the process exits non-zero (`<name> stopped
unexpectedly: exiting so the orchestrator restarts the service`) so the
orchestrator replaces the pod (`cmd/api/main.go`).

`cmd/mcp` and `cmd/a2a` run the SAME `GetSession` read use case over the same
Postgres database, run the idempotent embedded migrations at boot like
`cmd/api`, and never start the relay or a policy, never dial Kafka and never
call a sibling. There is NO authentication on either (fleet-wide decision,
[ADR 0005](/docs/adr/0005-mcp-server), [ADR 0007](/docs/adr/0007-a2a-inbound-adapter)):
access control is the in-cluster ClusterIP boundary plus whatever the gateway
allows.

## Dependencies

| Dependency | Used for | Source |
| --- | --- | --- |
| Postgres (via PgBouncer) | every write and read of all binaries; `DATABASE_URL` is the pooled DSN | Secret (`database.existingSecret`), key `DATABASE_URL` |
| Postgres (direct DSN) | boot migrations only; `MIGRATIONS_DATABASE_URL` | same Secret, key `MIGRATIONS_DATABASE_URL`, optional (falls back to `DATABASE_URL`) |
| Kafka (`KAFKA_BROKERS`) | outbox relay sink and the `/readyz` broker check (`cmd/api` only) | chart value `kafka.brokers` |
| shopping-cart (REST) | the saga reads the cart (`config.shoppingCartUrl`) | in-cluster sibling base URL |
| delivery-promise (REST) | pre-check the selected offers (`config.deliveryPromiseUrl`) | in-cluster sibling base URL |
| pricing (REST) | issue the binding price quote (`config.pricingUrl`) | in-cluster sibling base URL |
| customer-payments (REST) | authorize and void the payment (`config.customerPaymentsUrl`) | in-cluster sibling base URL |
| customer-ordering (REST) | place the order, the saga pivot (`config.customerOrderingUrl`) | in-cluster sibling base URL |
| OTel Collector | OTLP/gRPC traces and metrics | `otel.endpoint` (default `otel-collector.observability.svc.cluster.local:4317`) |

- All five sibling URLs are MANDATORY: `cmd/api` exits at boot naming the first
  missing one (`cmd/api/config.go`) and the chart refuses to render without
  them.
- The Collector is never on the request path: exporters dial lazily, so a
  Collector that is down costs telemetry and nothing else
  ([ADR 0006](/docs/adr/0006-fleet-observability-standard)).

## Deployment

### Helm chart

`charts/checkout` (`Chart.yaml` name `checkout`) renders the `api`
Deployment, Service, ConfigMap, Secret (only when `database.url` is given),
ServiceAccount and optional Ingress, Gateway API `HTTPRoute` and HPA, plus the
`mcp-*` and `a2a-*` Deployment, Service and route when enabled. The chart
**refuses to render** without `database.url` or `database.existingSecret`,
`kafka.brokers` and the five `config.*Url` values, naming the missing one.

```bash
helm install checkout ./charts/checkout \
  --set database.existingSecret=checkout-db \
  --set kafka.brokers=kafka:9092 \
  --set config.shoppingCartUrl=http://shopping-cart:80 \
  --set config.deliveryPromiseUrl=http://delivery-promise:80 \
  --set config.pricingUrl=http://pricing:80 \
  --set config.customerPaymentsUrl=http://customer-payments:80 \
  --set config.customerOrderingUrl=http://customer-ordering:80
```

Optional components:

```bash
helm upgrade --install checkout ./charts/checkout ... --set mcp.enabled=true --set a2a.enabled=true
```

`mcp.route.enabled=true` exposes the MCP server at the gateway path
`/api/mcp/checkout` (prefix rewritten to `/mcp`) on Service `checkout-mcp:8090`;
`a2a.route.enabled=true` exposes the agent at `/api/a2a/checkout` (prefix
rewritten to `/`) on Service `checkout-a2a:8093`, with the Agent Card at
`/api/a2a/checkout/.well-known/agent-card.json`. Requests to the A2A agent
carry the `A2A-Version` header. In-cluster without a route:
`http://checkout-mcp:8090/mcp` and `http://checkout-a2a:8093/`.
`A2A_AGENT_URL` defaults to `http://<release>-a2a.<ns>.svc.cluster.local:8093/`
and `a2a.agentUrl` overrides it.

How the fleet's local cluster deploys it (warehouse-infra `develop`,
`helm-values/checkout.yaml`): database Secret `checkout-db`
(`DATABASE_URL` and `MIGRATIONS_DATABASE_URL` keys), the five sibling URLs at
`http://<service>.warehouse-systems.svc.cluster.local:80`, a Gateway API route
at `/api/checkout` with the prefix stripped, and the A2A component enabled with
its route at `/api/a2a/checkout`. The MCP component is left off there.
`terraform/apps.yaml` registers `checkout` as a `service` with `database: true`.

Chart invariants are asserted by `charts/checkout/tests/` (`render_test.sh`,
`test_service_selectors.py`) and run in CI by the `helm-lint` and
`chart-tests` jobs: the env ConfigMap is mounted, mandatory values refuse to
render, `DATABASE_URL` is only ever a `secretKeyRef`, `extraEnv` refuses any
`*_MODE` and any `*_CONSUMER_GROUP`, every Service selects exactly one
Deployment, and the mcp and a2a Deployments never receive Kafka, sibling or
sweep variables. The packaging checklist is
[Before the first release](/docs/operations/before-first-release).

### Release

A push to `main` runs `docker-publish` (image `ghcr.io/iqvo/checkout:<short
sha>` and `latest`, cosign-signed, SBOM attested) and `release` (next
`vX.Y.Z` tag, image re-tagged, chart pushed to `oci://ghcr.io/iqvo`), after
every other CI job is green (see [Testing](/docs/development/testing#ci-jobs)).

### Probes and what readiness waits for

| Probe | Path | Settings |
| --- | --- | --- |
| startup | `GET /healthz` | every 2 s, 30 failures (about 60 s, covering the bounded Postgres retry) |
| liveness | `GET /healthz` | initial delay 5 s, every 10 s |
| readiness | `GET /readyz` | initial delay 3 s, every 5 s |

- `GET /healthz` is liveness only, on all three binaries. Never flipped by
  dependencies or by shutdown.
- `GET /readyz` (`cmd/api` only) is 200 `{"status":"ready"}` only while (a) the
  pod is not draining AND (b) a Postgres ping AND a Kafka broker metadata
  request both answer. The dependency verdict is cached 3 s and one round of
  checks is bounded 2 s (`internal/adapters/inbound/http/readiness.go`).
  While draining the answer is 503 `{"status":"not_ready"}`; while a
  dependency is down it is 503 `application/problem+json`, type
  `/problems/dependency-unavailable`, detail `not ready: postgres` or
  `not ready: kafka`. The cause is in the WARN log
  `readiness: dependency is not ready`.
- Kafka down: `/readyz` 503 but REST writes are still accepted; the events wait
  in `outbox_events`. Postgres down: `/readyz` 503 and REST writes fail. In
  both cases `/healthz` stays 200, so probes do not restart the pod.

### Graceful shutdown

Sequential (`cmd/api/main.go`, `cmd/api/workers.go`): `/readyz` flips to 503,
wait `SHUTDOWN_DRAIN_DELAY` (5 s), drain HTTP (10 s bound), stop the five
policies one after the other (10 s each), stop the relay (10 s), close the
pool. Worst case 75 s; the chart refuses a `terminationGracePeriodSeconds` that
does not exceed drain + 70 s (default 80). The relay stops last so nothing
committed by the policies is stranded.

### Migrations

One embedded migration, `0001_checkout_schema` (`internal/adapters/outbound/postgres/migrations`),
creates `checkout_sessions`, `idempotency_keys` and `outbox_events`.
Migrations run at boot, in all three binaries, over `MIGRATIONS_DATABASE_URL`
(falling back to `DATABASE_URL`), inside a bounded retry: 5 attempts with 1,
2, 4 and 8 s backoff (15 s total), because the first outbound dial of an
injected pod can be reset by the mesh sidecar. After the last attempt the
process refuses to boot and logs the last error. `schema_migrations` records
`version 1, dirty f`. There is no manual migration step.

## Kafka

This context **produces** two topics and **consumes none**:

| Topic | Events | Key | Consumers |
| --- | --- | --- | --- |
| `retailnetwork.checkout.events` | `CheckoutCompleted` | sessionId | shopping-cart, customer-payments (per `apis/asyncapi.yaml`) |
| `retailnetwork.checkout.analytics` | `CheckoutOpened`, `CheckoutDeclined`, `CheckoutRequoteRequired`, `CheckoutFailed`, `CheckoutSessionExpired`, `CheckoutPlacementInDoubt` | sessionId | none |

- **Consumer groups: none.** There is no consumer, so there is no group id to
  choose, reset or lag to monitor.
- **DLQ: none.** There is nothing to dead-letter on the consumer side. The
  producer-side equivalent is an outbox row that reached the attempt cap (see
  below).
- The writer uses `RequiredAcks=All`, a key-hash balancer (one aggregate on
  one partition, in order) and `AllowAutoTopicCreation`; the service never
  creates topics through an admin API. A broker with auto-creation off needs
  both topics created by hand.
- Every message is a CloudEvents 1.0 envelope in structured mode with header
  `content-type: application/cloudevents+json; charset=UTF-8`
  ([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)); the
  W3C `traceparent` of the request that raised the event is also stamped into
  the headers.

## Outbox relay

Every state change writes its events to `outbox_events` in the same Postgres
transaction as the session. The relay (`internal/adapters/outbound/outbox`)
then drains it:

- A pass claims up to 100 unpublished rows in `id` order with
  `FOR UPDATE SKIP LOCKED` (two replicas never claim the same row), sends them
  one at a time and marks each `published_at` after Kafka acknowledged it. A
  full batch is followed immediately by another pass; otherwise the relay
  sleeps `OUTBOX_RELAY_INTERVAL`.
- On the first send failure the pass stops (a later event of the same session
  must never overtake an earlier one), records `attempts + 1` and `last_error`
  on that row and logs `outbox relay pass failed`.
- Delivery is at-least-once. The CloudEvents `id` is minted once when the row
  is written, so a redelivery carries the same `id` for the consumer to
  de-duplicate.
- A row whose `attempts` reached `OUTBOX_MAX_ATTEMPTS` (20) is excluded from
  claims, logged once at ERROR (`outbox relay: row reached the attempt cap and
  is excluded from future claims; re-arm it with attempts = 0 after fixing the
  cause`, with `outbox_id`, `topic`, `event_type`, `key`) and later rows
  overtake it.

Inspect and re-arm:

```sql
SELECT count(*) FROM outbox_events WHERE published_at IS NULL;
SELECT id, event_type, subject, attempts, last_error
  FROM outbox_events WHERE published_at IS NULL ORDER BY id;
-- after fixing the cause (usually Kafka):
UPDATE outbox_events SET attempts = 0 WHERE id = <id>;
```

## The saga policies (sweepers)

| Policy | Tick | Finds | Does |
| --- | --- | --- | --- |
| `ResolveInDoubtPlacements` | 15 s (`INDOUBT_SWEEP_INTERVAL`) | `PLACING` sessions whose `next_attempt_at` is due | re-sends the stored placement request with the same `Idempotency-Key` (the sessionId); settles a definitive answer; alerts at 20 h; gives up and voids at 22 h |
| `ResolveStuckAuthorizations` | 30 s (`STUCK_SWEEP_INTERVAL`) | `AUTHORIZING` for more than 2 minutes | re-calls AuthorizePayment with the SAME key `<sessionId>:auth:<attempt>` and the kept token, which yields the true outcome |
| `RetryOwedVoids` | 1 m (`VOID_SWEEP_INTERVAL`) | sessions with `void_authorization_id` set | voids the authorization (`<sessionId>:void`) until it succeeds |
| `ExpireOnQuoteExpiry` | 1 m (`EXPIRY_SWEEP_INTERVAL`) | `OPEN` or `DECLINED` sessions past `quote_expires_at` | moves them to `EXPIRED` |
| `PurgeStoredPlacements` | 1 h (`PURGE_SWEEP_INTERVAL`) | closed sessions 24 h after `terminal_at` that still hold a placement body | drops the stored request (raises no event) |

Each tick drains its backlog in batches of 200 (a full batch means there may be
more). A failing tick is logged (`scheduled sweep failed; trying again at the
next tick`) and retried at the next one, never fatal. Several replicas are
safe: a session another actor already moved is skipped, and every transition
is an optimistic-version save.

Alerts are ERROR log lines `"msg":"ALERT"` with a stable `alert` field:
`placement-in-doubt-20h`, `placement-given-up-22h`, `place-order-rejected`,
`authorization-mismatch`, `sibling-contract-violation`. See
[Observability](/docs/operations/observability#alerts).

## Configuration in the chart

| Env var | Chart value | Default | Notes |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `config.httpAddr` | `:8080` | port must equal `service.targetPort` |
| `LOG_LEVEL` | `config.logLevel` | `info` | `debug`, `info`, `warn`/`warning`, `error` |
| `DATABASE_URL` | Secret key `database.existingSecretKey` | required | pooled DSN; never in the ConfigMap |
| `MIGRATIONS_DATABASE_URL` | Secret key `database.migrationsExistingSecretKey` | `DATABASE_URL` | direct DSN, boot migrations only |
| `KAFKA_BROKERS` | `kafka.brokers` | required | comma-separated |
| `SHOPPING_CART_URL` | `config.shoppingCartUrl` | required | |
| `DELIVERY_PROMISE_URL` | `config.deliveryPromiseUrl` | required | |
| `PRICING_URL` | `config.pricingUrl` | required | |
| `CUSTOMER_PAYMENTS_URL` | `config.customerPaymentsUrl` | required | |
| `CUSTOMER_ORDERING_URL` | `config.customerOrderingUrl` | required | |
| `SIBLING_TIMEOUT` | `config.siblingTimeout` | binary: `5s` | empty means not rendered |
| `CUSTOMER_ORDERING_TIMEOUT` | `config.customerOrderingTimeout` | binary: `15s` | empty means not rendered |
| `INDOUBT_SWEEP_INTERVAL` | `config.indoubtSweepInterval` | binary: `15s` | |
| `STUCK_SWEEP_INTERVAL` | `config.stuckSweepInterval` | binary: `30s` | |
| `VOID_SWEEP_INTERVAL` | `config.voidSweepInterval` | binary: `1m` | |
| `EXPIRY_SWEEP_INTERVAL` | `config.expirySweepInterval` | binary: `1m` | |
| `PURGE_SWEEP_INTERVAL` | `config.purgeSweepInterval` | binary: `1h` | |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` | binary: `1s` | |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` | `20` | `0` = unlimited |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` | binary: `5s` | whole seconds or minutes; sizes the grace period |
| `ENVIRONMENT` | `environment` | `production` | |
| `SERVICE_VERSION` | `image.tag` (else appVersion) | | |
| `OTEL_SERVICE_NAME` | `otel.serviceName` | `checkout` | rendered only with `otel.enabled=true`; mcp gets `<name>-mcp`, a2a `<name>-a2a` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel.endpoint` | in-cluster Collector | rendered only with `otel.enabled=true` |

The mcp component renders only `MCP_ADDR` (`mcp.httpAddr`), `LOG_LEVEL`,
`DATABASE_URL` / `MIGRATIONS_DATABASE_URL`, `SERVICE_VERSION`, `ENVIRONMENT`,
`OTEL_SERVICE_NAME` and `OTEL_EXPORTER_OTLP_ENDPOINT`; the a2a component the
same with `A2A_ADDR` (`a2a.httpAddr`) and `A2A_AGENT_URL` (`a2a.agentUrl`) in
place of `MCP_ADDR`. Anything else is never rendered into them; the selector
test fails the chart if it is.

## Idempotency-Key middleware

`internal/adapters/inbound/http/idempotency.go`:

- Both POST routes require an `Idempotency-Key` header of 1 to 128 characters.
- The first response below 500 is stored per `(route, key)` for 24 h
  (`internal/application/idempotency`); the request hash covers the method,
  path, query and body.
- A repeat with the same request replays the stored response with header
  `Idempotent-Replayed: true`; the same key with a different request is 422
  `idempotency-key-reused`. 5xx responses are never stored, so the key stays
  retryable.
- An expired record is ignored on read and overwritten by the next write for
  the same key, but no policy deletes `idempotency_keys` rows (see
  [Troubleshooting](/docs/operations/troubleshooting#idempotency_keys-grows)).

## Failure modes

### Pod crash-loop

- Symptom: restarts climb, the `WarehousePodRestarting` alert fires.
- Diagnosis: `kubectl -n warehouse-systems logs deploy/checkout --previous`
  shows `service exited with error` naming the missing variable
  (`cmd/api/config.go`), migration or ping exhaustion (`internal/bootretry`),
  or `<worker> stopped unexpectedly`. The mcp pod logs `mcp server exited with
  error`, the a2a pod `a2a agent exited with error`.
- Action: fix the named chart value or Secret key, then roll again.

### Postgres unreachable

- Symptom: `/readyz` 503 naming `postgres`; REST writes fail; the MCP server
  and A2A agent cannot serve sessions (every read is a Postgres query).
- Action: restore Postgres/PgBouncer. The relay and the policies resume on
  their own and nothing needs replaying; the pod is not killed. The readiness
  verdict refreshes within 3 s of recovery.

### Kafka unreachable

- Symptom: `/readyz` 503 naming `kafka`; REST writes still accepted and
  queued.
- Diagnosis: `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;`
  grows while brokers are down.
- Action: restore the brokers; the relay drains in `id` order automatically.
  Rows that reached the attempt cap need the re-arm statement above.

### A sibling is down

- Symptom: 503 `<sibling>-unavailable` from the POST routes
  (`shopping-cart-unavailable`, `delivery-promise-unavailable`,
  `pricing-unavailable`, `customer-payments-unavailable`,
  `customer-ordering-unavailable`). Open fails cleanly with no state change.
  A confirm whose authorization or place-order outcome is unknown answers 202
  and the session sits in `AUTHORIZING` or `PLACING` for the policies.
- Diagnosis: the request log line names the route and status; the WARN logs
  `authorization outcome unknown, the session stays AUTHORIZING` and the
  policy log lines name the session.
- Action: restore the sibling. The stuck and in-doubt policies resolve the
  half-finished sessions on their own; nothing to replay by hand.

### `PLACING` sessions that do not resolve

- Symptom: an ALERT `placement-in-doubt-20h`, then `placement-given-up-22h`.
- Diagnosis: `SELECT session_id, placement_attempts, next_attempt_at, updated_at
  FROM checkout_sessions WHERE state = 'PLACING';` (the backoff doubles from 30
  s to 15 min). Check customer-ordering reachability and whether it holds a
  result for `Idempotency-Key = <sessionId>`.
- Action: fix customer-ordering. At 22 h the session becomes `FAILED`
  (`placement-unresolved`) with the void owed, inside customer-payments' 24 h
  backstop and the network's 24 h idempotency window.

### Authorization mismatch

- Symptom: 409 `authorization-mismatch` on a confirm and an ALERT
  `authorization-mismatch`: customer-payments authorized an amount or
  currency that is not the quote total. The session returns to `DECLINED` with
  the void owed.
- Action: investigate customer-payments; the void is attempted at once and
  retried every minute by `RetryOwedVoids`.

### Owed voids that do not clear

- Diagnosis: `SELECT session_id, state, void_authorization_id FROM
  checkout_sessions WHERE void_authorization_id IS NOT NULL;`
- Action: restore customer-payments. A 404, or a 409 `illegal-transition`
  (already voided, captured or expired), counts as done.

### idempotency-key-reused storms

- Symptom: a burst of 422 `idempotency-key-reused`.
- Diagnosis: callers reuse a key while regenerating the body. Correct replays
  come back with `Idempotent-Replayed: true` and are not a fault.
- Action: fix the caller to keep the body byte-stable per logical request, or
  mint a fresh key.

## Routine procedures

- Restart: `kubectl -n warehouse-systems rollout restart deploy/checkout` (and
  `deploy/checkout-mcp` / `deploy/checkout-a2a` when enabled).
- Logs: Loki, `app=checkout` (JSON on stdout; raise `config.logLevel` to
  `debug` for a noisy pod).
- In-cluster REST: Service port 80 to container 8080; the external path is the
  gateway route `/api/checkout` (prefix stripped).
- Scale: the HPA is off by default (`autoscaling.api`). More than one replica
  is safe for the relay (row claims) and the saga writes (optimistic version
  saves) but has not been load-tested.
- Roll back: `helm rollback checkout`. The schema has one migration and no
  destructive step, so an older image runs on the same database.
- Look at one session: `GET /checkout-sessions/{sessionId}` (REST), the MCP
  tool `get_checkout_session` or the A2A skill `checkout_session_status`; all
  three read the same row.
