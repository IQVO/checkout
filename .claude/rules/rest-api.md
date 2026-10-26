---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Every route -> use case mapping, kept in sync with `apis/openapi.yaml` (routes live
in `internal/adapters/inbound/http/server.go`). This file is documentation for an
agent, not generated: keep it manually accurate.

- `POST /checkout-sessions` -> `OpenSession` (201; 409 `cart-not-checkout-ready`; 422 `offer-not-quotable`; 502 `upstream-contract-violation`; 503 `<sibling>-unavailable`)
- `GET /checkout-sessions/{sessionId}` -> `GetSession` (the polling endpoint while PLACING)
- `POST /checkout-sessions/{sessionId}:confirm` -> `ConfirmSession` (200 definitive; 202 while AUTHORIZING or PLACING; 409 `session-closed`, `authorization-mismatch`, `placement-in-doubt`, `void-pending`; 422 `price-quote-expired`)
- `GET /healthz` (liveness) and `GET /readyz` (drain gate + Postgres ping + Kafka metadata)

## Conventions

- Error shape: RFC 9457 `application/problem+json`, `type` = `/problems/<slug>`
  (catalogue in `errors.go`); every POST needs an `Idempotency-Key` (missing = 400
  `idempotency-key-required`, same key with another body = 422
  `idempotency-key-reused`, replay marked `Idempotent-Replayed: true`); a NUL in the
  path, query or body is a 400 before any key is recorded.
- Auth: none. Fleet-wide auth was deliberately reverted 2026-09-11; do not add
  bearer/JWT/API-key middleware (`TestNoAuthMiddlewareReintroduced`).
