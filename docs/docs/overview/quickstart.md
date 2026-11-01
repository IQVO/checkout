---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: Build Checkout, see what it refuses to boot without, run it on Postgres and Kafka and make the first REST, MCP and A2A calls. Every output on this page was captured from a real run.
---

# Quickstart

Build the three binaries, boot `cmd/api` (and see what it refuses to boot
without), run it against Postgres and Kafka, and make the first calls over
REST, MCP and A2A. Every output below was captured from real runs of the
binaries built from this branch (`go build ./cmd/api ./cmd/mcp ./cmd/a2a`),
with the five sibling services replaced by throwaway stand-ins (see
[the siblings](#the-siblings)).

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | `1.27.3` (`go.mod`) | everything |
| `make` | any | the targets below |
| Docker | any recent | the Postgres container below and `make integration` (testcontainers) |
| golangci-lint | `v2.14.0` (`GOLANGCI_VERSION` in the `Makefile`, pinned in CI) | `make lint`, `make check` |
| gremlins | `v0.6.0` (`GREMLINS_VERSION`) | `make mutation`, `make mutation-full` |
| govulncheck | latest | `make vuln` |
| Spectral (`@stoplight/spectral-cli`) | latest | linting the two specs, see [Testing](/docs/development/testing#ci-jobs) |
| Node 20 | | building this documentation site (`docs/`) |
| lefthook | optional | `lefthook install` wires `fmt-check vet lint` at pre-commit and `make check` at pre-push |

The fleet runs **one** Kafka broker, reachable at `localhost:9092` from the
kind cluster; the docker-compose Kafka is retired. This repository has no
compose file. The service needs Postgres **and** Kafka to boot and to be
ready; there is no mode without them.

## Build, test, run

```bash
make help          # list the targets
make build         # go build ./...
make test          # go test ./... -race
make arch-test     # go test ./internal/architecture/... -v
make bdd           # go test ./... -run TestFeatures -v   (34 scenarios in features/)
make coverage      # tests with -race plus the 90% gate over domain + application
make check         # fmt-check vet build lint test
make check-all     # check + coverage arch-test bdd
make integration   # go test -tags=integration ./... -race -count=1 (needs Docker)
```

Build the binaries into a scratch directory and start `cmd/api` with nothing
configured:

```bash
go build -o /tmp/checkout/ ./cmd/api ./cmd/mcp ./cmd/a2a
HTTP_ADDR=127.0.0.1:18080 /tmp/checkout/api
```

It refuses to boot, before it opens a connection or binds the port, and names
the first missing variable (exit status 1; real output):

```text
{"time":"2026-10-31T11:27:44.468867-03:00","level":"ERROR","msg":"service exited with error","error":"DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode"}
```

With a database but no sibling URLs, the next refusal is:

```text
{"time":"2026-10-31T11:27:46.416815-03:00","level":"ERROR","msg":"service exited with error","error":"SHOPPING_CART_URL is required: checkout calls this service synchronously"}
```

and a bad duration is refused too:

```text
{"time":"2026-10-31T11:27:50.952962-03:00","level":"ERROR","msg":"service exited with error","error":"SIBLING_TIMEOUT: \"soon\" is not a positive Go duration"}
```

## Run on Postgres and Kafka

Start a throwaway database and use the fleet's Kafka for the broker. The
service never creates topics through an admin API: the broker auto-creates
`retailnetwork.checkout.events` and `retailnetwork.checkout.analytics` on the
relay's first write (the writer sets `AllowAutoTopicCreation`). A broker with
auto-creation off needs them created by hand.

```bash
docker run -d --rm --name co-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=checkout \
  -p 127.0.0.1:55433:5432 postgres:16-alpine

export DATABASE_URL='postgres://postgres:pw@127.0.0.1:55433/checkout?sslmode=disable'
export KAFKA_BROKERS=localhost:9092
export SHOPPING_CART_URL=http://127.0.0.1:18081
export DELIVERY_PROMISE_URL=http://127.0.0.1:18082
export PRICING_URL=http://127.0.0.1:18083
export CUSTOMER_PAYMENTS_URL=http://127.0.0.1:18084
export CUSTOMER_ORDERING_URL=http://127.0.0.1:18085
HTTP_ADDR=127.0.0.1:18080 /tmp/checkout/api
```

Migrations run at boot (inside a bounded retry, `internal/bootretry`). The
startup log is JSON lines on stdout (`time` omitted here): `telemetry
configured` (with `otlp_endpoint` `localhost:4317`), `postgres adapters
configured`, one `scheduler running` line per policy (`job`, `interval`),
`outbox relay running` (`interval`, `max_attempts`, `events_topic`,
`analytics_topic`) and `http server listening`. The
[Observability](/docs/operations/observability) page lists every message.

### The siblings

`cmd/api` calls five services synchronously. In the cluster they are the real
services at their in-cluster URLs (see `helm-values/checkout.yaml` in
warehouse-infra). For a local run you need something at each URL. The calls
below were captured against a stand-in that answered:

| Sibling | Call | Stand-in answer |
| --- | --- | --- |
| shopping-cart | `GET /carts/cart-demo` | `OPEN` cart, two lines, one group with `offer-1` selected, readiness `true` |
| delivery-promise | `GET /delivery-offers/offer-1` | `QUOTED`, expires in one hour |
| pricing | `POST /price-quotes` | quote `quote-1`, USD, lines 2000 + 1500, delivery charge 500, tax 400, total 4400, 30 minutes |
| customer-payments | `POST /payment-authorizations` | `AUTHORIZED` for the requested total; a token starting `tok-declined` is `DECLINED` |
| customer-ordering | `POST /checkout` | `201 {"orderId":"ord-1"}`; for shopper `shopper-requote` a `409 offer-not-quotable` with re-quoted offers; for `shopper-down` a `503` |

## First calls

REST is unauthenticated. Probes first:

```bash
curl -si localhost:18080/readyz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}
```

`/readyz` is 200 only while Postgres answers a ping and a Kafka broker
answers a metadata request; `/healthz` is `{"status":"ok"}` and is never
affected by dependencies.

Open a session. Both write routes require an `Idempotency-Key` of 1 to 128
characters:

```bash
curl -si -X POST localhost:18080/checkout-sessions \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-open-1' \
  -d '{"cartId":"cart-demo","shopperId":"shopper-1","shipToAddressId":"addr-1"}'
```

```text
HTTP/1.1 201 Created
Content-Type: application/json

{"sessionId":"sess-3b5fcbd5535a","state":"OPEN","cartId":"cart-demo","cartVersion":3,"shopperId":"shopper-1","shipToAddressId":"addr-1","lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1}],"groups":[{"groupNo":1,"offerId":"offer-1","tier":"NEXT_DAY","lineNos":[1,2],"amountMinor":4400}],"priceQuoteId":"quote-1","currency":"USD","totalMinor":4400,"quoteExpiresAt":"2026-10-31T14:58:39Z","authorizationId":null,"orderId":null,"failureCode":null,"version":1}
```

Confirm it with a payment method token (never a card number):

```bash
curl -si -X POST 'localhost:18080/checkout-sessions/sess-3b5fcbd5535a/confirmations' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-confirm-1' \
  -d '{"paymentMethodToken":"tok-visa-demo"}'
```

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"sessionId":"sess-3b5fcbd5535a","state":"COMPLETED",...,"authorizationId":"auth-1","orderId":"ord-1","failureCode":null,"version":4}
```

`version` went from 1 to 4: `OPEN` to `AUTHORIZING` (2), to `PLACING` (3), to
`COMPLETED` (4). Observed answers of the other paths (all
`application/problem+json` for errors):

| Request | Answer |
| --- | --- |
| `POST /checkout-sessions` without `Idempotency-Key` | `400` `/problems/idempotency-key-required` |
| `POST /checkout-sessions` for `cart-unready` | `409` `/problems/cart-not-checkout-ready` |
| the same confirm again with the same key | `200`, same body, header `Idempotency-Replayed: true` |
| the same key with another token | `409` `/problems/idempotency-key-reused` |
| a new key on the `COMPLETED` session | `409` `/problems/session-closed` |
| `GET /checkout-sessions/sess-nope` | `404` `/problems/not-found` |
| confirm with a token starting `tok-declined` | `200`, `"state":"DECLINED"`; the session stays open for another token |
| confirm for shopper `shopper-requote` | `200`, `"state":"REQUOTE_REQUIRED"` with `requotedOffers` (offer `offer-2`, `TWO_DAY`, `deliverByEarliest` `2026-11-02`); the authorization is voided |
| confirm for shopper `shopper-down` | `202`, `"state":"PLACING"`: the place-order outcome is unknown |
| confirm again on that `PLACING` session | `409` `/problems/placement-in-doubt` |

A problem body looks like this (real answer to the missing key):

```json
{"type":"/problems/idempotency-key-required","title":"Idempotency-Key header is required","status":400,"detail":"send an Idempotency-Key header of 1 to 128 characters, unique per logical request","instance":"/checkout-sessions"}
```

### What the database and Kafka hold afterwards

```bash
docker exec co-pg psql -U postgres -d checkout -At \
  -c "select session_id,state,version,placement_attempts,next_attempt_at is not null from checkout_sessions order by created_at"
```

```text
sess-3b5fcbd5535a|COMPLETED|4|0|f
sess-c98e21dec639|REQUOTE_REQUIRED|5|0|f
sess-0af2272a5061|PLACING|4|1|t
sess-bb86021726ab|DECLINED|3|0|f
```

The `PLACING` row has one in-doubt attempt and a backoff set; the policy
`ResolveInDoubtPlacements` (a tick every 15 s) re-sends the stored request
until the sibling answers (the log line `scheduled sweep done` with
`"job":"ResolveInDoubtPlacements","handled":1`). The outbox after the run:

```bash
docker exec co-pg psql -U postgres -d checkout -At \
  -c "select id,split_part(event_type,'.',6),topic,attempts from outbox_events order by id"
```

```text
1|CheckoutOpened|retailnetwork.checkout.analytics|1
2|CheckoutCompleted|retailnetwork.checkout.events|1
...
6|CheckoutRequoteRequired|retailnetwork.checkout.analytics|1
7|CheckoutPlacementInDoubt|retailnetwork.checkout.analytics|1
8|CheckoutDeclined|retailnetwork.checkout.analytics|1
```

`attempts` 1 with `published_at` set means the relay delivered the row. The
public schema holds `checkout_sessions`, `idempotency_keys`, `outbox_events`
and `schema_migrations` (version 2, not dirty).

## The MCP server

`cmd/mcp` serves one read-only tool over Streamable HTTP:

```bash
MCP_ADDR=127.0.0.1:18090 /tmp/checkout/mcp
```

A real `initialize` (server name `checkout-mcp`), then `tools/list` and
`tools/call`, are on [MCP tools](/docs/mcp/tools). The call
`get_checkout_session` with `{"sessionId":"sess-3b5fcbd5535a"}` returns the
same session as REST; an unknown id answers `isError: true` with
`not-found: checkout session sess-nope: not found`.

## The A2A agent

`cmd/a2a` needs `A2A_AGENT_URL`, the URL it advertises:

```bash
A2A_ADDR=127.0.0.1:18093 A2A_AGENT_URL=http://127.0.0.1:18093/ /tmp/checkout/a2a
curl -s localhost:18093/.well-known/agent-card.json
```

The card names the agent `checkout`, lists one JSON-RPC interface at the
advertised URL and one skill, `checkout_session_status`. Send the skill
request as a data part:

```bash
curl -s -X POST localhost:18093/ -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' \
  -d '{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m-1","role":"ROLE_USER","parts":[{"data":{"skill":"checkout_session_status","arguments":{"sessionId":"sess-3b5fcbd5535a"}}}]}}}'
```

The answer is one agent message with two parts: a data part
(`{"skill":"checkout_session_status","data":{"session":{...}}}`) and a text
part: `sess-3b5fcbd5535a: COMPLETED, 2 line(s) in 1 group(s), total 4400 USD (minor units), quote expires 2026-10-31T14:58:39Z, order ord-1`.

## Stopping and cleaning up

Stop the binaries with Ctrl-C (`cmd/api` flips `/readyz` to 503, waits
`SHUTDOWN_DRAIN_DELAY`, drains HTTP, stops the policies, then the relay, and
closes the pool) and remove the container with `docker rm -f co-pg`.

## Building these docs

```bash
cd docs
npm ci
npm run build          # also fails on broken links and anchors
npm run gen-api-docs   # regenerate docs/api-reference/rest from apis/openapi.yaml
```

The REST reference pages are generated from `apis/openapi.yaml`; run the
generator after any spec change and commit the result, because `sidebars.ts`
imports the generated `sidebar.ts`.
