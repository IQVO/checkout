# CLAUDE.md — checkout

`checkout` is the **Checkout** bounded context (Supporting, `storefront` tier)
of the retail-network organisation's storefront domain (GitHub org `IQVO`, Go
module `github.com/IQVO/checkout`). It runs the purchase saga: one transaction
across price, payment and the network's promise confirmation. It authorizes
payment before placing the order (compensatable: void), places the order at
customer-ordering (the pivot), and leaves the rest to events. It owns the
`CheckoutSession` aggregate and holds `shopperId`/`shipToAddressId`
references, never PII and never card data. It ships three binaries:
`cmd/api` (REST + outbox relay + the clock-driven saga policies), `cmd/mcp`
(read-only MCP, `:8090`) and `cmd/a2a` (read-only A2A, `:8093`). No `web/`.

## Hard rules (each one cost an incident or is fitness-tested)

1. **Hexagonal dependency rule.** `internal/domain` depends on nothing internal
   but domain; `internal/application` only on domain and application; adapters
   never depend on each other; only `cmd/` wires layers. `make arch-test`
   (`TestHexagonalArchitecture`) enforces it.
2. **CloudEvents 1.0 is MANDATORY** on every Kafka message (structured mode, no
   flat envelope, no dual-write, no envelope toggle:
   `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestCloudEventsOnly`). Build
   only through `internal/adapters/kafka/cloudevents/`; `type` is
   `com.retailnetwork.storefront.checkout.checkoutsession.<Event>`, `source`
   `/retailnetwork/checkout`, subject and Kafka key = the sessionId. A breaking
   payload change is a new `.v2` type. Catalogue:
   `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`, checked against
   `apis/asyncapi.yaml` by `TestEventCatalogueMatchesContract`.
3. **Events leave through the transactional outbox only** (`ports.UnitOfWork`
   + `ports.EventOutbox`); the relay in `cmd/api` publishes, capped by
   `OUTBOX_MAX_ATTEMPTS` (`TestOutboxRelaysPassAttemptCap`). `CheckoutCompleted`
   is the one integration event (`retailnetwork.checkout.events`); the other six
   go to `retailnetwork.checkout.analytics`. This context consumes NO topic, so
   it has no consumer group.
4. **Sibling calls go only through the anti-corruption clients** of ADR 0003
   (`internal/adapters/outbound/` shoppingcart, deliverypromise, pricing,
   customerpayments, customerordering). customerordering is the ONLY place that
   knows the network's wire names. Only Open Host Service routes, never an
   in-org or restricted network route. No outbound call ever runs inside a
   database transaction.
5. **PLACING is the semantic lock.** While the place-order outcome is unknown
   (timeout, 5xx, `concurrent-update` 409) there is no void and no new attempt
   (`ErrPlacementInDoubt`); the confirm route answers 202 and
   `ResolveInDoubtPlacements` replays the stored, PII-free placement body byte
   for byte with the same `Idempotency-Key` (alert at 20 h, void at 22 h,
   inside the network's 24 h idempotency window). Never re-render the body.
6. **Every POST is behind `RequireIdempotencyKey`** (ADR 0009): a global
   1-128 printable-ASCII key; same key + fingerprint replays with
   `Idempotency-Replayed: true`, another fingerprint is 409
   `idempotency-key-reused`; 5xx answers are never stored. Keys sent to
   siblings stay within 128 characters. Errors are RFC 9457 problems
   `/problems/<slug>`.
7. **Real Postgres and Kafka only.** `cmd/api` refuses to boot without
   `DATABASE_URL`, `KAFKA_BROKERS` and the five sibling base URLs; the memory
   adapters are test doubles (`TestRuntimeBinaryHasNoInMemoryAdapters`).
   Integration tests use testcontainers only.
8. **No auth layer on REST, MCP or A2A** (fleet-wide revert 2026-09-11):
   `TestNoAuthMiddlewareReintroduced`. MCP and A2A are read-only adapters over
   `GetSession` (`TestMCPAdapterDependencyRule`,
   `TestA2AAdapterIsReadOnlyAndHasNoOutboundClient`); opening and confirming
   a session stay on REST because they move money.
9. **Requoted dates are the destination's calendar days** (ADR 0008): format
   in each offer's `timeZone`, fall back to UTC; `cmd/api/tzdata.go` embeds
   the zone database.
10. **An ADR comes first** for a new cross-context contract, a new consumed
    event, a new sibling call or any auth. ADRs live in `docs/adr/`.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages: run before saying "done"
make check        # fmt-check vet build lint test
make check-all    # check + coverage (90% gate on domain+application) + arch-test + bdd
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins over every internal/domain package (CI job mutation-fast, blocking)
make vuln         # govulncheck ./...
make guide-lint   # these guides: skills load, references resolve, context budget
```

Run locally: `DATABASE_URL=... KAFKA_BROKERS=... SHOPPING_CART_URL=...
DELIVERY_PROMISE_URL=... PRICING_URL=... CUSTOMER_PAYMENTS_URL=...
CUSTOMER_ORDERING_URL=... go run ./cmd/api` (REST `:8080`; migrations run at
boot); `DATABASE_URL=... go run ./cmd/mcp`; `DATABASE_URL=... A2A_AGENT_URL=...
go run ./cmd/a2a`.

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`: ubiquitous language, the aggregate, its
  states and errors, events, use cases and policies (always loaded).
  `.claude/rules/rest-api.md`: routes and error slugs.
  `.claude/rules/integration-events.md`: published types, topics, envelope.
- `.claude/rules/fleet/*.md`: fleet-wide rules copied from the harness
  template; never edit them here.
- `docs/adr/`: decisions (index: `docs/adr/index.md`); ADR 0003 is the sibling
  call table and outcome mapping. `apis/openapi.yaml`, `apis/asyncapi.yaml`:
  contracts (`api-lint` runs the Spectral `rest-guideline` ruleset from
  `.spectral.yaml`). Operating docs: `docs/docs/operations/` (configuration,
  runbook, observability, troubleshooting), `docs/docs/development/testing.md`,
  `docs/docs/ecosystem/integration.md`. `HARNESS.md`: what each sensor runs.
- `charts/checkout/`: the Helm chart (api always on; MCP and A2A opt-in).

harness-template: v4
(hooks, guide-lint).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/outbox/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
