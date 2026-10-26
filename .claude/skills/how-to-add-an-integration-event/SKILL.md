---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event in this service: CloudEvents 1.0 type naming, AsyncAPI, the type catalogue ADR, the encoder, the transactional outbox, the inbound consumers. Use when touching internal/adapters kafka or outbox code, a domain event, or apis/asyncapi*.yaml.
---

# How to add an integration event

checkout publishes seven events (one integration event, six analytics-only) and consumes
none (ADR 0003, ADR 0004). The envelope is CloudEvents 1.0 only; read
`.claude/rules/integration-events.md` and `.claude/rules/fleet/cloudevents.md` first. The
worked example for publishing is `CheckoutCompleted` (integration) and `CheckoutFailed`
(analytics).

## Publishing a new event

1. **Domain event.** A typed struct in `internal/domain/checkoutsession/events.go`
   (`CheckoutFailed`): embeds `shared.Header`, fields exactly the model's `data` list,
   methods `EventName()`, `Entity()` (`checkoutsession`, lowercase, no separators) and
   `SubjectID()` (the sessionId), plus a `shared.Event` assertion. The aggregate command
   (`RecordFailure` in `terminals.go`) raises it.
2. **Contract, one commit.** Add the message and its `data` schema to
   `apis/asyncapi.yaml`, AND one
   `type: com.retailnetwork.storefront.checkout.checkoutsession.<Event> stream: analytics`
   line (or `stream: events`) to
   `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`.
   `TestEventCatalogueMatchesContract` fails when the two disagree, so the push fails
   when they are not in the same commit. Lint:
   `spectral lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml`.
   Evolution is additive only; a breaking change is a new `:v2` dataschema. Check the
   consumers' AsyncAPI (shopping-cart, customer-payments) still reads what you send.
3. **Encoder.** In `internal/adapters/outbound/kafka/payloads.go` add the payload struct
   (camelCase json tags, instants as `time.Time`) and a `case` in `payloadFor`; route it in
   `encoder.go` `route` (only `CheckoutCompleted` goes to the events topic). The envelope
   itself is built by `internal/adapters/kafka/cloudevents` only; never hand-build one.
   Add a golden test and `testdata/<Event>.golden.json` (exact JSON of every attribute and
   the payload) in `encoder_test.go` (`go test ./internal/adapters/outbound/kafka -update`
   rewrites the goldens; read the diff).
4. **Publish from the use case** through `Deps.persist` inside the same
   `ports.UnitOfWork` as the `Save` (`internal/application/usecases/deps.go`): the
   aggregate row and the outbox row commit or roll back together. Never publish after
   the commit and never call Kafka from a use case.
5. **Nothing else.** The relay (`internal/adapters/outbound/outbox/relay.go`) already
   drains every row.
6. **Prove it.** `go test -tags=integration ./internal/adapters/outbound/outbox/ -race -count=1`
   reads events back from a real broker; extend `cmd/api/e2e_flow_integration_test.go`
   when the event is part of a REST flow.

## Consuming an event from a sibling context

Checkout consumes nothing today, because the saga reads its siblings synchronously. Adding
a consumer is an architectural decision: write an ADR that amends ADR 0003 first
(`.claude/skills/how-to-write-an-adr/SKILL.md`), then:

1. **Read the producer's contract now:**
   `git -C ~/warehouse-systems/<producer> show origin/feature/service:apis/asyncapi.yaml`.
   The full `type` string must be byte-identical.
2. **Vendor a tolerant copy** into `apis/asyncapi.yaml` (a `receive` message with only the
   fields you read).
3. **Consumer group.** A consumer needs its own mandatory `*_CONSUMER_GROUP` variable
   (boot error naming it, read in `cmd/api/config.go`), never a literal
   (`TestKafkaConsumerGroupNeverHardcodedInline`); consume with `cloudevents.Decode`,
   dispatch on the full type, dedupe on the CloudEvents `id` through an inbox table, and
   dead-letter a payload that can never be applied (`TestDeadLetterCallsGuardCancellation`).
4. **Prove it** with a dispatch test, a flat-message-rejected test and a testcontainers
   Kafka integration test.

## Verify before opening the PR

`make check`, `go test ./internal/architecture/ -count=1` (catalogue, CloudEvents-only,
relay cap), the spectral lint of the AsyncAPI, and the integration command above.
