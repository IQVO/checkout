# checkout

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Checkout** (Supporting, storefront tier).

The purchase saga [14][16]: one transaction across price, payment and the network's promise confirmation. Authorize before placing the order (compensatable: void), place the order (pivot), then retriable steps via events. PLACING is the semantic lock [15]: while the place-order outcome is unknown there is no void and no new attempt. Holds shopperId and shipToAddressId references, never PII; the stored placement request is PII-free so it can be replayed byte for byte.

Status: built to the model (domain, application, Postgres, Kafka, REST, MCP, A2A, BDD); the PR is awaiting review. The design lives in [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/) `storefront/model/model.yaml` (single source of truth).
This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its
published `retailnetwork.*` events; it never calls an in-org or restricted network route.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.storefront.checkout.<entity>.<Event>`), transactional outbox.

## Observability

Logs, metrics and traces follow the fleet standard (ADR 0006): JSON logs on stdout with `trace_id`/`span_id` and a request line per HTTP call (`/healthz` and `/readyz` excluded), OTLP/gRPC metrics and traces to the Collector, `traceparent` stamped into outbox headers. Environment: `OTEL_SERVICE_NAME` (`checkout`, `checkout-mcp`, `checkout-a2a`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`, `LOG_LEVEL`. Export never blocks: without a Collector telemetry is dropped. The business counter is `checkout.sessions.confirmed` (`outcome`, `reason`).

## A2A agent

`cmd/a2a` (ADR 0007, `internal/adapters/inbound/a2a`) is a **read-only** Agent2Agent agent, the third deployable beside `cmd/api` and `cmd/mcp`, over the same `GetSession` use case. A2A carries delegation only; facts still leave as CloudEvents from `cmd/api`. One skill, `checkout_session_status` (derived from the MCP tool `get_checkout_session`): send `{"skill":"checkout_session_status","arguments":{"sessionId":"sess-..."}}` as a data part. Rejections come back in-band as RFC 7807-style problems (404/400/500), never as JSON-RPC errors. Opening and confirming a session stay on REST (they move money). No auth (fleet decision of 2026-09-11).

- Layout: `cmd/a2a/` (composition root), `internal/adapters/inbound/a2a/` (server, skills, mapping).
- Environment: `A2A_ADDR` (default `:8093`), `A2A_AGENT_URL` (**mandatory**, absolute http(s) URL advertised on the card), `DATABASE_URL` (**mandatory**), `MIGRATIONS_DATABASE_URL`, plus the shared telemetry variables.
- Endpoints: JSON-RPC at `/` and `/a2a`, card at `/.well-known/agent-card.json`, `/healthz`.
- Kubernetes: `a2a.enabled=true` (default off) renders `a2a-deployment/service/route`; `a2a.route.enabled=true` exposes `/api/a2a/checkout` (prefix rewritten to `/`); `A2A_AGENT_URL` defaults to `http://<release>-a2a.<ns>.svc.cluster.local:8093/` and `a2a.agentUrl` overrides it.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
