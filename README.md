# checkout

[![CI](https://github.com/IQVO/checkout/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/IQVO/checkout/actions/workflows/ci.yml?query=branch%3Adevelop) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/checkout/blob/develop/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/checkout/blob/develop/go.mod) [![release](https://img.shields.io/github/v/release/IQVO/checkout?sort=semver)](https://github.com/IQVO/checkout/releases) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/checkout/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/checkout/blob/develop/LICENSE)

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Checkout** (Supporting, storefront tier).

The purchase saga [14][16]: one transaction across price, payment and the network's promise confirmation. Authorize before placing the order (compensatable: void), place the order (pivot), then retriable steps via events. PLACING is the semantic lock [15]: while the place-order outcome is unknown there is no void and no new attempt. Holds shopperId and shipToAddressId references, never PII; the stored placement request is PII-free so it can be replayed byte for byte.

Status: built to the model (domain, application, Postgres, Kafka, REST, MCP, A2A, BDD). This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its published `retailnetwork.*` events; it never calls an in-org or restricted network route.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

This repo has no docs site of its own. The context's design lives in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/) — `storefront/model/model.yaml` there is the single source of truth.

Page map:

| Area | Pages |
|---|---|
| Context pack | [Checkout context page](https://iqvo.github.io/warehouse-docs/storefront/contexts/checkout) and its 13-page ddd-crew pack (business context, ubiquitous language, core-domain chart, bounded-context canvas, context map, aggregate canvas, domain events, message flow, eventstorming, use cases, class/sequence/ER diagrams) |
| Strategic design | [Storefront strategic design](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/) — subdomain classification, context map, CloudEvents event standard, model gap register |
| API reference | [REST reference generated from `apis/openapi.yaml`](https://iqvo.github.io/warehouse-docs/storefront/api-reference/rest/checkout/) |
| Decisions | [Storefront ADR index](https://iqvo.github.io/warehouse-docs/storefront/adr/) |
| In this repo | [`docs/adr/`](docs/adr/) (the repo's own ADRs), [`docs/operations/runbook.md`](docs/operations/runbook.md), [`docs/before-first-release.md`](docs/before-first-release.md) |

## Architecture

Hexagonal / ports & adapters; dependencies point **inward only**.

```
cmd/api/                    composition root: REST API, outbox relay, the five clock-driven
                            saga policies, all on real Postgres and real Kafka
cmd/mcp/                    read-only MCP server over the same GetSession use case (ADR 0005)
cmd/a2a/                    read-only A2A agent over the same GetSession use case (ADR 0007)
internal/
  domain/                   pure business logic: checkoutsession (the aggregate), shared
  application/
    ports/                  OUT interfaces only (repos, EventPublisher, SessionMetrics, Clock)
    usecases/               one struct per use case
    exchange/               the saga's exchange/mapping logic
    idempotency/            idempotency-key pool
    outbox/                 transactional outbox
    views/                  read views
  adapters/
    inbound/                http (chi), mcp, a2a, requestlog, scheduler
    outbound/               postgres, kafka, outbox relay, telemetry, httpx, the five
                            sibling anti-corruption clients (shoppingcart, pricing,
                            customerpayments, customerordering, deliverypromise —
                            ADR 0003), alerts, clock, ids, memory (test doubles), repocontract
    kafka/cloudevents/      the CloudEvents 1.0 Kafka writer
  architecture/             the fitness tests below
  bootretry/                bounded Postgres/migration boot retry
  bdd/                      godog wiring for features/
```

There is no in-memory mode and no log sink: `cmd/api` needs a database and a
broker, and exits at boot naming the missing variable. The in-memory adapters
are test doubles only. The layering is enforced as an executable test, not a
convention: `internal/architecture/architecture_test.go`
(`TestHexagonalArchitecture`, arch-go) fails the build if the domain ever
imports an outer layer or an adapter reaches sideways into a sibling; the same
package adds fitness tests for the event catalogue vs `apis/asyncapi.yaml`,
the A2A adapter's read-only rule, Kafka writer acks/topics/DLQ shutdown, the
outbox relay cap, and no-Go-files-at-root.

## Domain events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml); the type catalogue is ADR 0004.

**Published** — seven events, all through the transactional outbox (at-least-once,
stable CloudEvents `id` for dedupe):

- `CheckoutCompleted` → `retailnetwork.checkout.events` — the one integration
  event; consumed by `shopping-cart` and `customer-payments`. Kafka key = the sessionId.
- `CheckoutOpened`, `CheckoutDeclined`, `CheckoutRequoteRequired`,
  `CheckoutFailed`, `CheckoutSessionExpired`, `CheckoutPlacementInDoubt` →
  `retailnetwork.checkout.analytics` — analytics-only, no network consumer.

`ConfirmSession` and `RecordAuthorization` raise nothing.

**Consumed** — nothing: the saga is driven by REST calls and policies; sibling
data is read synchronously through the anti-corruption clients (ADR 0003).

**Envelope** (mandatory): CloudEvents 1.0, structured content mode, Kafka header
`content-type: application/cloudevents+json; charset=UTF-8`; `source` =
`/retailnetwork/checkout`, `subject` and Kafka key = the sessionId. Type
convention, real examples:

```
com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted
com.retailnetwork.storefront.checkout.checkoutsession.CheckoutPlacementInDoubt
```

A breaking payload change is a new `.v2` type, never a mutation.

## Running it

Real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` and the five
sibling base URLs are mandatory; a boot error names the missing variable).
Migrations run automatically at boot (bounded retry via `internal/bootretry`).
There is no compose file in this repo — point `DATABASE_URL` and
`KAFKA_BROKERS` at any Postgres/Kafka (the fleet's `warehouse-infra` kind
cluster or the integration tests' testcontainers both work).

```sh
export DATABASE_URL="postgres://user:***@localhost:5432/checkout?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export SHOPPING_CART_URL="http://localhost:8081"
export DELIVERY_PROMISE_URL="http://localhost:8082"
export PRICING_URL="http://localhost:8083"
export CUSTOMER_PAYMENTS_URL="http://localhost:8084"
export CUSTOMER_ORDERING_URL="http://localhost:8085"
go run ./cmd/api      # REST on :8080; relay + the five policies in the same process
```

`GET /readyz` is 200 only while Postgres answers a ping AND a Kafka broker
answers a metadata request; `/healthz` is liveness only.

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | REST listen address |
| `DATABASE_URL` | **required** | Postgres DSN; unset ⇒ boot error naming the variable |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct (non-pooled) DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers (relay, `/readyz`) |
| `SHOPPING_CART_URL` | **required** | Base URLs of the five services the saga calls |
| `DELIVERY_PROMISE_URL` | **required** | (boot error naming the variable when one is missing) |
| `PRICING_URL` | **required** | |
| `CUSTOMER_PAYMENTS_URL` | **required** | |
| `CUSTOMER_ORDERING_URL` | **required** | |
| `SIBLING_TIMEOUT` | `5s` | Per-call timeout of the siblings (Go duration) |
| `CUSTOMER_ORDERING_TIMEOUT` | `15s` | Timeout of the place-order call (Go duration) |
| `INDOUBT_SWEEP_INTERVAL` | `15s` | Policy ResolveInDoubtPlacements tick |
| `STUCK_SWEEP_INTERVAL` | `30s` | Policy ResolveStuckAuthorizations tick |
| `VOID_SWEEP_INTERVAL` | `1m` | Policy RetryOwedVoids tick |
| `EXPIRY_SWEEP_INTERVAL` | `1m` | Policy ExpireOnQuoteExpiry tick |
| `PURGE_SWEEP_INTERVAL` | `1h` | Stored-placement purge tick |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `checkout` | `service.name` of spans and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | in-cluster Collector | OTLP/gRPC endpoint; export never blocks |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

`cmd/mcp` reads `MCP_ADDR` (default `:8090`), `DATABASE_URL` (**required**),
`MIGRATIONS_DATABASE_URL` (default `DATABASE_URL`), `LOG_LEVEL`, and the shared
telemetry variables (`OTEL_SERVICE_NAME` defaults to `checkout-mcp`).

`cmd/a2a` reads `A2A_ADDR` (default `:8093`), `A2A_AGENT_URL` (**required**,
absolute http(s) URL advertised on the Agent Card), `DATABASE_URL`
(**required**), `MIGRATIONS_DATABASE_URL` (default `DATABASE_URL`),
`LOG_LEVEL`, and the shared telemetry variables (`OTEL_SERVICE_NAME` defaults
to `checkout-a2a`).

## Observability

Logs, metrics and traces follow the fleet standard (ADR 0006): JSON logs on
stdout with `trace_id`/`span_id` and a request line per HTTP call (`/healthz`
and `/readyz` excluded), OTLP/gRPC metrics and traces to the Collector,
`traceparent` stamped into outbox headers. Export never blocks: without a
Collector telemetry is dropped. Environment: `OTEL_SERVICE_NAME` (`checkout`,
`checkout-mcp`, `checkout-a2a`), `OTEL_EXPORTER_OTLP_ENDPOINT`,
`SERVICE_VERSION`, `ENVIRONMENT`, `LOG_LEVEL`.

| Instrument | Kind | Source |
|---|---|---|
| `checkout.sessions.confirmed` | counter, attributes `outcome`, `reason` | **this service's business metric** |
| HTTP request duration | histogram + one server span and request-log line per call | the HTTP adapter, sharing a trace id |
| `go.*` runtime metrics | various | OTel Go runtime instrumentation |

## API

Contract: [`apis/openapi.yaml`](apis/openapi.yaml). No authentication. Errors
are RFC 9457 problem details (`application/problem+json`) whose `type` is
`/problems/<slug>`; shared slugs are declared once in `components`. Every
write takes an `Idempotency-Key` header — a retry with the same key replays
the stored placement request byte for byte (alert at 20 h, give up and void at
22 h, inside the network's 24 h idempotency window).

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/checkout-sessions` | Open a checkout session for a cart |
| `GET` | `/checkout-sessions/{sessionId}` | Get a checkout session |
| `POST` | `/checkout-sessions/{sessionId}:confirm` | Confirm the session with a payment method token |
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness (Postgres ping + Kafka metadata) |

The confirm call places the order at customer-ordering `POST /checkout` with an
`Idempotency-Key` equal to the session's, so a replay can never double-place.

## Helm chart

[`charts/checkout`](charts/checkout) — one always-on component: the `api`
Deployment (REST, the outbox relay and the five clock-driven saga policies in
one process); the read-only MCP server and the read-only A2A agent are optional
extra components. The chart refuses to render without `database.*`,
`kafka.brokers` and the five `config.*Url` sibling base URLs (named in the
failure). `DATABASE_URL` always comes from a Secret; the api renders only the
`config.*` keys mapped 1:1 to the variables above; `extraEnv` refuses any
`*_MODE` and any `*_CONSUMER_GROUP` variable (this context consumes no topic).

```sh
helm install checkout ./charts/checkout \
  --set database.existingSecret=checkout-db \
  --set kafka.brokers=kafka:9092 \
  --set config.shoppingCartUrl=http://shopping-cart:80 \
  --set config.deliveryPromiseUrl=http://delivery-promise:80 \
  --set config.pricingUrl=http://pricing:80 \
  --set config.customerPaymentsUrl=http://customer-payments:80 \
  --set config.customerOrderingUrl=http://customer-ordering:80
```

Telemetry is the `otel` values block (`enabled: true` by default, safe where no
Collector is deployed), `SERVICE_VERSION` is the image tag (chart `appVersion`
when empty) and `ENVIRONMENT` is `environment`. Chart invariants are asserted
by `charts/checkout/tests/` (`render_test.sh`, `test_service_selectors.py`),
run in CI by the `helm-lint` (`ct lint`) and `chart-tests` jobs.

### Running the MCP server in Kubernetes

The MCP server (ADR 0005) ships in the same image as `/app/mcp` and is a
separate, opt-in deployable: `mcp.enabled=true` renders a `<release>-mcp`
Deployment and ClusterIP Service on port `8090`. The pod reads the same
`DATABASE_URL` secret as the api, runs the OLTP migrations on start
(idempotent), and serves MCP over Streamable HTTP (`/mcp`) plus `GET /healthz`
for the probes. Its surface is **read-only**: one tool,
`get_checkout_session` (the session state `OPEN`, `AUTHORIZING`, `DECLINED`,
`PLACING`, `REQUOTE_REQUIRED`, `COMPLETED`, `EXPIRED` or in-doubt, with lines,
groups and totals — the same read use case as
`GET /checkout-sessions/{sessionId}`); no write use case is wired, so this
binary can never insert into the outbox, never starts the relay or the
policies and never dials Kafka. There is NO auth of any kind (fleet-wide
revert 2026-09-11); with `mcp.route.enabled=true` the gateway exposes it at
`/api/mcp/checkout`, rewritten to `/mcp`.

```sh
helm upgrade --install checkout ./charts/checkout \
  --set database.existingSecret=checkout-db \
  --set kafka.brokers=kafka:9092 \
  --set mcp.enabled=true
```

### Running the A2A agent in Kubernetes

`cmd/a2a` (ADR 0007, `internal/adapters/inbound/a2a`) is a **read-only**
Agent2Agent agent, the third deployable beside `cmd/api` and `cmd/mcp`, over
the same `GetSession` use case. A2A carries delegation only; facts still leave
as CloudEvents from `cmd/api`. One skill, `checkout_session_status` (derived
from the MCP tool `get_checkout_session`): send
`{"skill":"checkout_session_status","arguments":{"sessionId":"sess-..."}}` as
a data part. Rejections come back in-band as RFC 7807-style problems
(404/400/500), never as JSON-RPC errors. Opening and confirming a session stay
on REST (they move money). No auth (fleet decision of 2026-09-11).

- Layout: `cmd/a2a/` (composition root), `internal/adapters/inbound/a2a/` (server, skills, mapping).
- Endpoints: JSON-RPC at `/` and `/a2a`, card at `/.well-known/agent-card.json`, `/healthz`.
- Kubernetes: `a2a.enabled=true` (default off) renders `a2a-deployment/service/route`; `a2a.route.enabled=true` exposes `/api/a2a/checkout` (prefix rewritten to `/`); `A2A_AGENT_URL` defaults to `http://<release>-a2a.<ns>.svc.cluster.local:8093/` and `a2a.agentUrl` overrides it.

## Local development / quality gate

Every CI sensor is a `make` target; `make help` lists them all.

```sh
make check-fast  # fmt-check, vet, arch-test (+ tests of changed packages)
make check       # fmt-check, vet, build, lint, test
make check-all   # check + coverage (90% gate over domain+application) + arch-test + bdd
make integration # testcontainers Postgres/Kafka — needs only Docker
make mutation    # gremlins over ./internal/domain
make vuln        # govulncheck ./...
```

Git hooks are managed by [lefthook](https://github.com/evilmartians/lefthook)
(`lefthook.yml`): pre-commit runs `fmt-check` + `vet` + `lint`; pre-push runs
`make check`. Activate once per clone:

```sh
brew install lefthook   # or: go install github.com/evilmartians/lefthook@latest
lefthook install
```

## Tests

```sh
go test ./... -race                        # unit suites
make coverage                              # domain+application coverage, CI gate >= 90%
go test -tags=integration ./...            # testcontainers Postgres/Kafka (Docker only)
go test ./features/ -run TestFeatures -v   # godog/BDD over the real HTTP API
go test ./internal/architecture/... -v     # architecture fitness tests (arch-go)
gremlins unleash ./internal/domain         # mutation, thresholds in .gremlins.yaml
```

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`,
`test` (unit + the 90% coverage gate), `bdd`, `integration`, `mutation-fast`
(the blocking subset), `api-lint` (Spectral over `apis/openapi.yaml`), `vuln`
(govulncheck), `helm-lint` (`ct lint`), `chart-tests`, and `arch-test` on
every push (branches `main`, `develop`) and pull request. The exhaustive
`mutation` job runs only on the weekly schedule (Monday 06:00 UTC) and manual
dispatch, opening a `harness:red` issue on a scheduled failure.
`docker-publish` (GHCR, cosign-signed) and `release` (tag `vX.Y.Z` + chart
publish) run only on pushes to `main`, after every other job is green.

## Architecture Decision Records

[`docs/adr/`](docs/adr/) — 7 ADRs (0001 bounded context, 0002 assumptions,
0003 synchronous contracts and the anti-corruption layer, 0004 CloudEvents
envelope and type catalogue, 0005 MCP server, 0006 fleet observability
standard, 0007 A2A inbound adapter).

## License

[MIT](LICENSE).
