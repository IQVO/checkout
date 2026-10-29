# checkout

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Checkout** (Supporting, storefront tier).

The purchase saga [14][16]: one transaction across price, payment and the network's promise confirmation. Authorize before placing the order (compensatable: void), place the order (pivot), then retriable steps via events. PLACING is the semantic lock [15]: while the place-order outcome is unknown there is no void and no new attempt. Holds shopperId and shipToAddressId references, never PII; the stored placement request is PII-free so it can be replayed byte for byte.

Status: built to the model (domain, application, Postgres, Kafka, REST, MCP, BDD); the PR is awaiting review. The design lives in [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/) `storefront/model/model.yaml` (single source of truth).
This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its
published `retailnetwork.*` events; it never calls an in-org or restricted network route.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.storefront.checkout.<entity>.<Event>`), transactional outbox.

## Observability

Logs, metrics and traces follow the fleet standard (ADR 0006): JSON logs on stdout with `trace_id`/`span_id` and a request line per HTTP call (`/healthz` and `/readyz` excluded), OTLP/gRPC metrics and traces to the Collector, `traceparent` stamped into outbox headers. Environment: `OTEL_SERVICE_NAME` (`checkout`, `checkout-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`, `LOG_LEVEL`. Export never blocks: without a Collector telemetry is dropped. The business counter is `checkout.sessions.confirmed` (`outcome`, `reason`).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
