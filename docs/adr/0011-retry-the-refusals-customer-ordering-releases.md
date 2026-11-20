# ADR 0011: Retry the refusals customer-ordering releases under the session's Idempotency-Key (the consumer half of the replay-class rule)

## Status

Accepted (2026-11-19). Refines the "Outcome mapping of PlaceOrder" of
[ADR 0003](./0003-synchronous-contracts-and-the-anti-corruption-layer.md). It adopts the consumer side of the
fleet idempotency rule decided on 2026-11-18 (warehouse-harness-template:
`.claude/rules/fleet/idempotency-and-outbox.md`, `docs/idempotency-replay-classes.md`). The callee's side is
merged: customer-ordering ADR 0019 (PR #91). No route, status code, problem slug, field, event or schema of
this service changes.

## Context

checkout calls customer-ordering `POST /checkout` with the deterministic key `Idempotency-Key = sessionId`.
Before the rule customer-ordering stored every refusal below 500 for 24 hours, so a refusal stuck to the
session: sending it again replayed it, and ADR 0003 read a 409 `promise-capacity-exhausted` or `book-closed`
as a definitive requote (`REQUOTE_REQUIRED`) and a 400/422 as a contract bug (`FAILED`, `place-order-rejected`)
at once. After the rule customer-ordering does NOT store a refusal whose answer depends on state that can
change (replay class RETRYABLE): nothing was committed, the key is released, and the same request under the
same key executes again and may pass. A book reopens, capacity frees up, a shopper is registered by another
request, delivery-promise answers again. checkout ended the session on the first of them.

## Decision

1. **The outbound adapter reads the callee's class.** `internal/adapters/outbound/customerordering/refusal_classes.go`
   holds one visible table, derived from customer-ordering's merged class table, that maps the problem slug of
   `POST /checkout` to retryable or terminal. `Gateway.Place` sets `exchange.PlaceOutcome.Released` (and `Slug`)
   from it; the use cases and the domain see only the flag. The class is the callee's knowledge and lives in the
   anti-corruption layer, never in the domain.
2. **Unknown is terminal.** A slug that is not in the table, a 4xx without a problem body and a slug of another
   service are `Released = false`: an unknown refusal never loops. "Terminal" keeps the handling the refusal had
   before the rule: a definitive 409 or a 400/422 ends the session at once, any other answer (a 5xx, a 404, a
   409 `concurrent-update`, an unknown 409) stays an unknown outcome, in doubt, exactly as it was.
3. **What is retried.** A released refusal is recorded like an unknown outcome: the session stays `PLACING`
   (`RecordPlacementInDoubt`: the semantic lock holds, no void, the confirm route answers 202 and a new confirm is
   `ErrPlacementInDoubt`), `ResolveInDoubtPlacements` sends the stored, PII-free body again with the SAME key
   (the session id) after the existing backoff (30 s doubling to 15 min), and a later success completes the
   session normally (`CheckoutCompleted`). Only the resolver talks to customer-ordering after the first call, and
   it sends the stored body byte for byte, so a released key is never reused with another body.
4. **Bounds: the existing ones, unchanged.** The in-doubt budget: the ALERT `placement-in-doubt-20h` at 20 h and
   the give-up at 22 h, inside the network's 24 h idempotency window. Nothing is added to the schedule: no new
   loop, timer, column or limit. At the bound a refusal that is still released is read as it was before the rule,
   so the session ends in **the same terminal state as today**: `REQUOTE_REQUIRED` for a 409 requote,
   `FAILED` `place-order-rejected` (with the alert) for a 400/422, the void owed in both. An unknown outcome still
   ends `FAILED` `placement-unresolved`.
5. **The reason is recorded in the log.** Every released refusal is a WARN `customer-ordering released a refusal,
   the placement stays PLACING and is retried under the same key` with `sessionId`, `slug`, `detail` (status, slug
   and the problem's detail) and `attempts`; the next one replaces it in the log, a success or the terminal
   outcome closes the story. `placement_attempts` and `next_attempt_at` of the session count the passes. The
   session has no field for the reason and the shopper-visible `Session` schema has none: surfacing it would be a
   contract change, reported below and NOT made.
6. **The honest table.** `refusal_spec_test.go` holds the table against the byte-pinned copy of customer-ordering's
   OpenAPI (`contracts/pinned.json`): the Idempotency-Key text of `POST /checkout` lists the problem types that are
   not stored ("retry with the same key") and the ones that are stored ("use a new key"), and its 4xx and 503
   responses name the rest. A named slug the table leaves out, a released one it calls terminal or a stored one it
   calls retryable fails; so does a rewording of the sentences. `refusal_classes_test.go` holds the table equal to a
   copy of the callee's ADR 0019 class table (19 catalogue entries and the three key problems).

### The table

| Class here | Problem types of customer-ordering (class at the callee) |
| --- | --- |
| retryable | `not-found`, `concurrent-update`, `illegal-transition`, `cancel-after-lock`, `validation-failed`, `promise-capacity-exhausted`, `book-closed`, `group-spans-nodes`, `promise-destination-mismatch`, `delivery-promise-unavailable` (RETRYABLE, 10) |
| terminal | `offer-not-quotable` (OUTCOME); `malformed-request`, `line-without-promise`, `status-regression`, `shopper-exists`, `too-many-addresses`, `invalid-limit`, `invalid-cursor`, `unknown-query-parameter` (FINAL, 8); `idempotency-key-required`, `idempotency-key-invalid`, `idempotency-key-reused` (this service sends a valid key and the same body every time: repeating it cannot pass) |

What changes for the session, by the answer of `POST /checkout`:

| Answer | Before | After |
| --- | --- | --- |
| 409 `promise-capacity-exhausted`, `book-closed` | `REQUOTE_REQUIRED` at once, void | `PLACING`, retried; `REQUOTE_REQUIRED` at 22 h if still refused |
| 422 `validation-failed`, `group-spans-nodes`, `promise-destination-mismatch` | `FAILED` `place-order-rejected` at once, void, alert | `PLACING`, retried; `FAILED` `place-order-rejected` at 22 h if still refused |
| 409 `concurrent-update`, 404 `not-found`, 503 `delivery-promise-unavailable` | in doubt, retried | unchanged (the table agrees; the slug now shows in the error) |
| 409 `offer-not-quotable` | `REQUOTE_REQUIRED` at once | unchanged (OUTCOME: stored, the shopper re-quotes under a new session) |
| 400 `malformed-request`, 422 `line-without-promise`, unknown slug, 4xx without a body | `FAILED` at once | unchanged |

## Consequences

- A refusal that used to end the session now keeps it `PLACING` for as long as the refusal is released, at most
  22 h. The authorization stays held and the shopper polls a session that answers 202 meanwhile. For
  `promise-capacity-exhausted` and `book-closed` that is slower for the shopper than the immediate requote (which
  carried no offers for these two slugs: only `offer-not-quotable` carries `requotedOffers`); whether a shorter
  bound or a visible reason is wanted is a product decision, reported, not taken here.
- **Shopper-driven retry.** A shopper cannot re-submit a session after a released refusal: the session is
  `PLACING`, a confirm is refused with `placement-in-doubt` and sends nothing, and a closed session stays closed.
  The key `sessionId` is therefore never freed and reused with another body by this service (a released key with
  another body would be a new request at customer-ordering, not `idempotency-key-reused`).
  `TestTheShopperCannotRetryAReleasedRefusalWithAnotherBody` pins it.
- A callee that stored a refusal before its own deploy of the rule may still replay it from its 24 h store; the
  retry then repeats the refusal until the bound, harmlessly.
- **Not covered here.** The other deterministic keys are not mapped, because those callees have not merged their
  classes: `<sessionId>:auth:<attempt>` and `<sessionId>:void` (customer-payments) and
  `priceQuoteKey(requestKey)` (pricing). A follow-up per callee, with its own table and honest-table test, once
  its ADR is merged. This service's OWN idempotency classes (one OUTCOME candidate, `authorization-mismatch`) are a
  separate change.
- **Contract change needed to show the reason to the shopper (not made).** `Session` (`apis/openapi.yaml`) has no
  field for a pending reason, so the shopper sees `PLACING`. A field such as `lastRefusal` (slug) on the session is
  a REST contract change and a stored-session change, and belongs in its own decision.
