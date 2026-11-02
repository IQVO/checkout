---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
description: Symptom, cause, check and fix for the failure modes of Checkout, taken from the error paths in the code.
---

# Troubleshooting

Each entry is symptom, cause, how to check, how to fix. The problem
documents are `application/problem+json` (RFC 9457) whose `type` is
`/problems/<slug>`; the slug is the thing to search for. Operational
background is in the [Runbook](/docs/operations/runbook); every log message
named here is listed in [Observability](/docs/operations/observability).

## The process does not start or does not stay up

### `service exited with error`: a variable is named

- **Cause:** a mandatory variable is missing or malformed. `cmd/api` checks, in
  order, `DATABASE_URL`, `KAFKA_BROKERS`, the five sibling URLs
  (`SHOPPING_CART_URL`, `DELIVERY_PROMISE_URL`, `PRICING_URL`,
  `CUSTOMER_PAYMENTS_URL`, `CUSTOMER_ORDERING_URL`; absolute URL required),
  then `SIBLING_TIMEOUT` and `CUSTOMER_ORDERING_TIMEOUT` (positive Go duration).
- **Check:** the `error` field of the log line names the variable; in
  Kubernetes `kubectl logs deploy/checkout --previous`.
- **Fix:** set it (chart values `config.*`, `kafka.brokers`, `database.*`).
  The chart refuses to render without them, so this usually means a hand-made
  manifest or a changed Secret.

### `A2A_AGENT_URL is required` / `is not an absolute http(s) URL`

- **Cause:** `cmd/a2a` must advertise the URL clients dial on its Agent Card; a
  missing, relative or schemeless value is refused at boot, together with a
  missing `DATABASE_URL`.
- **Fix:** set `A2A_AGENT_URL` (the chart default is the in-cluster Service
  DNS; `a2a.agentUrl` overrides it).

### Boot fails with `run migrations (after 5 attempts)` or `ping postgres (after 5 attempts)`

- **Cause:** Postgres (or the direct migration DSN) was unreachable for the
  whole 15 s retry budget (`internal/bootretry`), or the credentials are wrong.
  The last real error is wrapped in the message.
- **Check:** the preceding WARN `retrying` lines (`op`, `attempt`, `err`);
  connect with the same DSN. A `MIGRATIONS_DATABASE_URL` that points at
  PgBouncer in transaction mode can break migrations: it must be a direct DSN.
- **Fix:** repair the DSN or the network. If a migration was interrupted and
  `schema_migrations` shows `dirty = t`, repair the schema by hand and clear
  the flag; the migrations are `0001_checkout_schema` and `0002_idempotency_key_global`.

### `<scheduler job> stopped unexpectedly` / `outbox-relay stopped unexpectedly`

- **Cause:** a worker returned without being asked. The process exits
  non-zero on purpose so the orchestrator restarts it.
- **Check:** the ERROR just before it (`scheduler stopped`, `outbox relay
  stopped`) carries the error.
- **Fix:** fix the cause shown; look at crash-loop restarts
  (`WarehousePodRestarting`).

### The pod is killed during a rollout

- **Cause:** `terminationGracePeriodSeconds` is shorter than the sequential
  shutdown (drain delay + 10 s HTTP + 10 s per policy x 5 + 10 s relay).
- **Fix:** keep the chart default (80) or more; the chart refuses a value that
  does not exceed the sum for the configured `SHUTDOWN_DRAIN_DELAY`.

## Readiness and dependencies

### `/readyz` is 503 `dependency-unavailable` (`not ready: postgres` / `not ready: kafka`)

- **Cause:** the Postgres ping or the Kafka metadata request failed (or timed
  out at 2 s). The verdict is cached 3 s.
- **Check:** the WARN `readiness: dependency is not ready` has `dependency`
  and the real `error`. `/healthz` is still 200, so the pod is not restarted.
- **Fix:** restore the dependency. With Kafka down, REST writes still succeed
  and the events queue in `outbox_events`.

### `/readyz` is 503 `{"status":"not_ready"}`

- **Cause:** the pod is draining (SIGTERM received). Expected during a
  rollout.

### Every request is slow or `statement timeout`

- **Cause:** each connection has `statement_timeout = 5s` and the pool is
  capped at 10 connections per process (`internal/adapters/outbound/postgres/pool.go`).
- **Check:** slow queries on Postgres; the `duration_ms` of the request lines.
- **Fix:** the database; scale replicas only after the cause is found.

## REST errors, by slug

| Status and slug | Cause | Check and fix |
| --- | --- | --- |
| `400` `idempotency-key-required` | the POST has no `Idempotency-Key` | send a key of 1 to 128 printable ASCII characters, unique per logical request |
| `400` `idempotency-key-invalid` | the `Idempotency-Key` is longer than 128 characters or has a character outside printable ASCII | mint a UUID per logical submit |
| `400` `malformed-request` | invalid JSON, an unknown field, trailing data, an empty body or a body over 64 KiB; also a NUL (U+0000) in the path, query or body | the `detail` names the decoder error; the schemas are strict (`DisallowUnknownFields`) |
| `404` `not-found` | no such session, or a `{sessionId}` longer than 128 characters | the id is `sess-...` from the open answer |
| `409` `cart-not-checkout-ready` | the cart is not `OPEN` or its `checkoutReadiness` is false (a group is unselected or an offer expired) | fix the cart in shopping-cart, then open again |
| `409` `session-closed` | the session is `COMPLETED`, `EXPIRED`, `FAILED` or `REQUOTE_REQUIRED` | those never change; open a new session. `DECLINED` is not closed: confirm again with another token |
| `409` `placement-in-doubt` | the session is `PLACING`: the place-order outcome is unknown, there is no void and no new attempt | poll `GET /checkout-sessions/{sessionId}`; `ResolveInDoubtPlacements` settles it |
| `409` `void-pending` | a previous authorization of this session is still owed a void | retry shortly; `RetryOwedVoids` runs every minute |
| `409` `authorization-mismatch` | customer-payments authorized an amount or currency that is not the quote total; the session returned to `DECLINED` and the authorization is being voided | ALERT `authorization-mismatch`; investigate customer-payments |
| `409` `illegal-transition` | the command does not apply in the session's current state (for example a confirm while `AUTHORIZING`) | wait for the stuck-authorization policy or poll |
| `409` `idempotency-key-reused` | the same `Idempotency-Key` with a different method, path or body (the key is global, not per route) | mint a fresh key, or keep the body byte-stable; never retry blindly |
| `409` `concurrent-update` | another writer got in between load and save twice (each command retries once) | retry with the same key |
| `422` `validation-failed` | `cartId`, `shopperId` or `shipToAddressId` missing, an invalid id or token, or the cart does not exist (shopping-cart answered 404) | the `detail` names the field |
| `422` `price-quote-expired` | the binding quote has expired at confirm | open a new session |
| `422` `offer-not-quotable` | at open, a selected delivery offer is not `QUOTED` or expires within 2 minutes | refresh the delivery quote in the cart and open again |
| `422` `price-quote-rejected` | pricing answered 4xx to the quote request (an unpriced item) | check pricing for the cart's items |
| `502` `upstream-contract-violation` | a sibling answered something that cannot form a consistent session (groups that do not cover the lines, a quote that does not add up to the total) | ALERT `sibling-contract-violation`; the `detail` names what is wrong; fix the sibling |
| `503` `<sibling>-unavailable` | `shopping-cart`, `delivery-promise`, `pricing`, `customer-payments` or `customer-ordering` timed out, was unreachable or answered 5xx | retry with the same key; see the next section |
| `500` `internal-error` | an error that is mapped to nothing; the detail never leaks the message | the ERROR `request failed` has the real error and the `trace_id` |

## The saga is not finishing

### A confirm answered 202 and the session stays `AUTHORIZING`

- **Cause:** the AuthorizePayment outcome is unknown (timeout, transport error,
  a 409 or a 5xx from customer-payments). The session was saved before the
  call and keeps the token.
- **Check:** WARN `authorization outcome unknown, the session stays
  AUTHORIZING`; `SELECT session_id, authorizing_since FROM checkout_sessions
  WHERE state = 'AUTHORIZING'`.
- **Fix:** nothing by hand. After 2 minutes `ResolveStuckAuthorizations`
  re-calls with the SAME key `<sessionId>:auth:<attempt>`, which yields the
  true outcome, and the saga carries on.

### The session stays `PLACING`

- **Cause:** customer-ordering timed out, was unreachable or answered 5xx or a
  retriable 409. The stored, PII-free request is replayed byte for byte with
  `Idempotency-Key = sessionId`, so a pass that raced a slow first call cannot
  place a second order.
- **Check:** `SELECT placement_attempts, next_attempt_at FROM checkout_sessions
  WHERE session_id = '...'` (backoff 30 s doubling to 15 min); the policy logs.
- **Fix:** restore customer-ordering. At 20 h the ALERT
  `placement-in-doubt-20h` fires; at 22 h the session becomes `FAILED`
  (`placement-unresolved`) and the authorization is voided.

### `REQUOTE_REQUIRED` or `FAILED` and the money

- **Cause:** by design the authorization is voided: the decision is saved in
  the same change (`void_authorization_id`), the void is attempted at once and
  `RetryOwedVoids` retries every minute.
- **Check:** `SELECT session_id, state, void_authorization_id FROM
  checkout_sessions WHERE void_authorization_id IS NOT NULL`. A non-empty
  column long after the close means customer-payments keeps failing.
- **Fix:** restore customer-payments; customer-payments' own 24 h backstop
  voids unbound authorizations in any case.

### A requote shows delivery dates one day off

- **Cause:** the window of a requoted offer is a span of instants; the shopper
  reads it as days in the destination's calendar. The ACL formats each bound in
  the offer's `timeZone` and falls back to UTC (WARN `requoted offer time zone
  unusable; delivery dates fall back to UTC`) when the name is missing, is
  `Local` or is unknown ([ADR 0008](/docs/adr/0008-requoted-dates-in-the-destination-time-zone)).
  `cmd/api` embeds the zone database (`cmd/api/tzdata.go`).
- **Fix:** have customer-ordering send a valid IANA `timeZone`.

## Events are late, missing or duplicated

### Events do not reach Kafka

- **Cause:** the relay cannot publish. The row stays in `outbox_events` with
  `attempts` and `last_error`; at `OUTBOX_MAX_ATTEMPTS` (20) it is excluded and
  logged once at ERROR.
- **Check:** `SELECT id, event_type, attempts, last_error FROM outbox_events
  WHERE published_at IS NULL ORDER BY id`; the ERROR `outbox relay pass
  failed`.
- **Fix:** restore Kafka (or create the topics when the broker has
  auto-creation off), then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>`
  for capped rows.

### A consumer sees the same event twice

- **Cause:** delivery is at-least-once: a crash between the Kafka
  acknowledgement and the `published_at` update republishes the row.
- **Fix:** de-duplicate on the CloudEvents `id`, which is stable across
  redelivery (it is persisted with the row).

### Events of one session arrive out of order

- **Cause:** events of one session share a key and a partition, so they are
  ordered within a topic, but `CheckoutCompleted` (events topic) and the other
  six (analytics topic) are different topics.
- **Fix:** do not rely on order across the two topics.

## Data growth

### `idempotency_keys` grows

- **Cause:** an expired record (older than 24 h) is ignored on read and
  overwritten by the next write for the same key, but no policy deletes rows.
- **Fix:** if the table matters, delete old rows by `created_at` out of band;
  nothing in the service depends on them after 24 h.

## MCP and A2A

- **MCP tool answers `isError: true` with `not-found: ...`:** the session id is
  unknown or longer than 128 characters. Read-only; nothing to fix.
- **A2A answers HTTP 200 with a problem inside the message:** by design a
  rejection is a business answer, not a JSON-RPC error. The data part carries
  `problem` with status 400 (`malformed-skill-request`, `invalid-argument`,
  `unknown-skill`), 404 `not-found` or 500 `internal-error`.
- **Neither server sees a session `cmd/api` just wrote:** they read the same
  database; check that `DATABASE_URL` points at the same one.
- **Agent Card advertises the wrong URL:** `A2A_AGENT_URL` is advertised
  verbatim; set it to the URL clients dial (gateway or in-cluster).

## Telemetry

- **No `trace_id` in a log line:** the record was written without a
  span-carrying context (boot and shutdown lines have none).
- **No traces or metrics at all:** the Collector is unreachable. Export is
  non-blocking and silent; set `LOG_LEVEL=debug` to see the exporter errors
  (`opentelemetry error`). `OTEL_EXPORTER_OTLP_ENDPOINT` defaults to
  `localhost:4317` outside the chart.
