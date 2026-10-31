---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 1
description: Every environment variable each Checkout binary reads, with default, required flag and meaning, and how invalid values are handled.
---

# Configuration

Checkout is configured only through environment variables. There is no config
file and no flag. Each binary reads its own set; a variable that several
binaries read has a row in each table with that binary's own default.

Rules common to all three binaries:

- **Misconfiguration is refused at boot**, before a connection is opened or a
  port is bound, and the error names the variable. `cmd/api` additionally has
  no in-memory mode and no log-only sink: without Postgres and Kafka it does
  not start (`cmd/api/config.go`, [ADR 0002](/docs/adr/0002-checkout-assumptions)).
- Defaults below are the ones in the code. The Helm chart sets some of them
  differently (for example `ENVIRONMENT=production` and the in-cluster
  Collector address); the chart value that renders each variable is listed in
  the [Runbook](/docs/operations/runbook#configuration-in-the-chart).
- Durations are Go durations (`500ms`, `5s`, `1m`, `1h`). The test-only
  variables of the integration suites are not listed.

## `cmd/api` (REST, outbox relay, saga policies)

| Variable | Default | Required | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | REST listen address. The chart requires its port to equal `service.targetPort`. |
| `DATABASE_URL` | none | **yes** | Postgres DSN (the pooled one behind PgBouncer in the cluster). Unset is the boot error `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (session-mode) DSN used only by the embedded migrations at boot. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list for the outbox relay and the `/readyz` broker check. Blank entries are dropped; an empty list is the boot error `KAFKA_BROKERS is required`. |
| `SHOPPING_CART_URL` | none | **yes** | Base URL of shopping-cart (`GET /carts/{cartId}`). Must be an absolute URL (scheme and host), else `<VAR>: "<value>" is not an absolute URL`. |
| `DELIVERY_PROMISE_URL` | none | **yes** | Base URL of delivery-promise (`GET /delivery-offers/{offerId}`). |
| `PRICING_URL` | none | **yes** | Base URL of pricing (`POST /price-quotes`). |
| `CUSTOMER_PAYMENTS_URL` | none | **yes** | Base URL of customer-payments (authorize and void). |
| `CUSTOMER_ORDERING_URL` | none | **yes** | Base URL of customer-ordering (`POST /checkout`). |
| `SIBLING_TIMEOUT` | `5s` | no | Per-call timeout of the four fast siblings. Not a positive Go duration is a boot error. |
| `CUSTOMER_ORDERING_TIMEOUT` | `15s` | no | Timeout of the place-order call, which promises and confirms every shipment group before it answers. Not a positive Go duration is a boot error. |
| `INDOUBT_SWEEP_INTERVAL` | `15s` | no | Tick of the policy `ResolveInDoubtPlacements`. Invalid or non-positive: warning `invalid INDOUBT_SWEEP_INTERVAL; using the default`. |
| `STUCK_SWEEP_INTERVAL` | `30s` | no | Tick of `ResolveStuckAuthorizations`. Same fallback rule. |
| `VOID_SWEEP_INTERVAL` | `1m` | no | Tick of `RetryOwedVoids`. Same fallback rule. |
| `EXPIRY_SWEEP_INTERVAL` | `1m` | no | Tick of `ExpireOnQuoteExpiry`. Same fallback rule. |
| `PURGE_SWEEP_INTERVAL` | `1h` | no | Tick of `PurgeStoredPlacements`. Same fallback rule. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Sleep of the outbox relay after a pass that did not fill a batch. A Go duration or plain seconds (`2`, `0.5`). Invalid or non-positive: warning and the default. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures after which the relay stops claiming an outbox row. `0` is unlimited. Negative or unparsable: warning and `20`, so a typo never removes the cap. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait after `/readyz` flips to 503 before the listener closes. `0` disables it. Negative or unparsable: warning and `5s`. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive. Anything else is `info`. |
| `OTEL_SERVICE_NAME` | `checkout` | no | `service.name` of spans, metrics and the `app` label of the logs. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector, `host:port` or a URL. A bare `host:port` becomes plaintext `http://`. Export never blocks. |
| `SERVICE_VERSION` | `dev` | no | `service.version` of the telemetry resource. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` of the telemetry resource. |

## `cmd/mcp` (read-only MCP server)

| Variable | Default | Required | Meaning |
| --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address of the Streamable HTTP server (`/mcp` and `/`) and `GET /healthz`. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database that `cmd/api` owns. Unset is the boot error `DATABASE_URL is required: this context runs on real Postgres`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations (the embedded migrations are idempotent, so the MCP pod may run them too). |
| `LOG_LEVEL` | `info` | no | As for `cmd/api`. |
| `OTEL_SERVICE_NAME` | `checkout-mcp` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | As for `cmd/api`. |
| `SERVICE_VERSION` | `dev` | no | `service.version`. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`. |

This binary reads no Kafka, sibling or sweep variable: it never starts the
relay or a policy and never dials Kafka or a sibling
([ADR 0005](/docs/adr/0005-mcp-server)).

## `cmd/a2a` (read-only A2A agent)

| Variable | Default | Required | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address of the JSON-RPC server (`/` and `/a2a`), the Agent Card and `GET /healthz`. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL of this agent's JSON-RPC endpoint, advertised on the Agent Card (in the cluster `http://<release>-a2a.<ns>.svc.cluster.local:8093/`). Missing or not absolute is a boot error naming the variable, raised before telemetry and any connection. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database. Reported together with `A2A_AGENT_URL` when both are missing. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `LOG_LEVEL` | `info` | no | As for `cmd/api`. |
| `OTEL_SERVICE_NAME` | `checkout-a2a` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | As for `cmd/api`. |
| `SERVICE_VERSION` | `dev` | no | `service.version`, also reported as the Agent Card `version`. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`. |

## Things that are not configurable

- **No consumer group.** This context consumes no topic, so there is no
  `*_CONSUMER_GROUP` and the chart refuses one in `extraEnv`.
- **No mode switch.** There is no `*_MODE` variable; the chart refuses one.
- **No authentication settings.** REST, MCP and A2A are unauthenticated
  fleet-wide; there is nothing to configure.
- **Topic names** (`retailnetwork.checkout.events`, `retailnetwork.checkout.analytics`)
  are constants in `internal/adapters/outbound/kafka/encoder.go`.
- **Timings of the saga** are constants of the domain, not settings: stuck
  authorization after 2 minutes, in-doubt alert at 20 h, give-up at 22 h,
  stored-placement purge 24 h after a closed state, offer margin 2 minutes,
  in-doubt backoff from 30 s doubling up to 15 minutes
  (`internal/domain/checkoutsession`).
- `OTEL_RESOURCE_ATTRIBUTES` is honoured by the OpenTelemetry SDK resource
  detector in addition to the variables above.
