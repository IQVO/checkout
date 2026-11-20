# ADR 0011: Every problem type has a replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-19). Adopts the fleet rule decided by the owner on 2026-11-18 (standard:
warehouse-harness-template `docs/idempotency-replay-classes.md`). Supersedes one sentence of
[ADR 0009](/docs/adr/0009-adopt-the-fleet-rest-api-guideline) item 2 ("A 2xx or 4xx answer is stored for 24
hours"); the rest of ADR 0009 stands.

## Context

`RequireIdempotencyKey` (`internal/adapters/inbound/http/idempotency.go`) stored the first answer below 500 of
every key, errors included, and replayed it for 24 hours. A refusal that a retry could pass was therefore pinned:
a `cart-not-checkout-ready` 409 replayed to a shopper who fixed the cart and retried with the same key, a
`concurrent-update` 409 replayed to a client that did the right thing.

The store is a **separate write**, not part of the business transaction (read before this change, same lines
before and after): the middleware takes a per-process key lock, calls `store.Get`, replays on a hit, otherwise
runs the handler into a capture buffer and calls `store.Put` in its own transaction
(`postgres/idempotency.go`: `INSERT ... ON CONFLICT (key) DO UPDATE ... WHERE` the existing row is older than the
TTL). The use cases commit through their own units of work before the handler answers. Consequences:

- **Releasing is skipping the write.** Nothing is claimed beforehand, so a RETRYABLE refusal releases its key by
  not calling `Put`. There is no rollback of a key row.
- **OUTCOME here means "stored, and the use case has committed".** There is no "the writes commit with the key
  row" because they never shared a transaction; ADR 0009 already records that a crash between the commit and the
  key write loses the stored answer. A retry then meets the new state (never a second authorization: the
  downstream keys derive from the session id).
- A 5xx is not stored but the use cases' units of work may already have committed (unchanged).
- Across replicas both execute and the first `Put` wins; the loser replays the winner's answer (pre-existing,
  unchanged, reported not fixed).

## Decision

Every problem type declares exactly one replay class on its catalogue entry (the `class` field of `problem`,
`internal/adapters/inbound/http/errors.go`); a problem cannot be built without one (`newProblem` is the only
constructor, an AST test forbids literals elsewhere) and a test fails on an unclassified entry.

| Class | Meaning | Middleware |
| --- | --- | --- |
| `FINAL` | a function of the request alone (or of state that only moves one way for it) | stored and replayed |
| `OUTCOME` | the command committed state, then answered an error that must be remembered | stored and replayed; the use case has committed |
| `RETRYABLE` | nothing committed; the answer depends on state that can change | not stored, the key is released, a retry executes |

`writeProblem` (every problem, `writeError` included) marks the middleware's capture with the class; the
middleware stores a 2xx, or a refusal marked FINAL or OUTCOME, and nothing else. A 5xx and a panic are never
stored; a refusal that reaches the middleware without a class is released like RETRYABLE. The middleware holds no
slug list and no status range beyond 2xx and 5xx. 2xx, 5xx and FINAL answers are byte-identical to before.
Releasing a RETRYABLE key means a different body under the same key afterwards is a new request, not
`idempotency-key-reused`. When unsure between FINAL and RETRYABLE the class is RETRYABLE.

**A concurrent duplicate when the first ends RETRYABLE.** Requests with one key are serialised by the in-process
key lock: the duplicate waits, finds nothing stored, and executes the command itself; the copies behind it replay
its answer when it is stored. Across replicas both execute (see above).

### The classes (held equal to the catalogue by `TestTheADRDeclaresTheClassOfEveryProblemType`)

| Problem type | Class | Status | Reason (code) |
| --- | --- | --- | --- |
| `malformed-request` | FINAL | 400 | the request alone: `decodeJSON` (`response.go`), the body bound (`idempotency.go`), a NUL in the query or body (`nul.go`) |
| `idempotency-key-required` | FINAL | 400 | the header alone; answered before the store, never stored |
| `idempotency-key-invalid` | FINAL | 400 | the header alone; answered before the store, never stored |
| `idempotency-key-reused` | FINAL | 409 | the request and the stored fingerprint; it is the answer of a hit, never stored |
| `resource-not-found` | FINAL | 404 | a NUL in the path: the request alone (`nul.go`), answered before the middleware |
| `not-found` | RETRYABLE | 404 | the session of the confirm path (`deps.go` mutate `Sessions.Get`, minted here, never deleted) or an identifier too long to name one (`handlers.go` `pathID`); the slug is shared with a session or cart another request creates. J1 |
| `cart-not-checkout-ready` | RETRYABLE | 409 | shopping-cart's readiness, another context's state that the shopper fixes (`open_session.go` 34-36, `open.go` 86-88, `shoppingcart/gateway.go` `toCart`); nothing committed |
| `session-closed` | FINAL | 409 | COMPLETED, EXPIRED, FAILED and REQUOTE_REQUIRED never change (`transitions.go` 30-31, `session.go` `Closed`); nothing leaves a closed state. J3 |
| `authorization-mismatch` | OUTCOME | 409 | `recordAuthorization` saves the session DECLINED with the void owed (`confirm_session.go` 87-99, `transitions.go` 80-83), settles the void (108), then returns the error (109). The retry must not authorize again. |
| `placement-in-doubt` | RETRYABLE | 409 | PLACING is the semantic lock (`transitions.go` 42-44); `ResolveInDoubtPlacements` moves the session on; nothing committed |
| `void-pending` | RETRYABLE | 409 | a void is still owed (`transitions.go` 48-50); `RetryOwedVoids` clears it every minute; nothing committed |
| `price-quote-expired` | FINAL | 422 | `quoteExpiresAt` is fixed when the session opens (`open.go`) and the clock only moves forward (`transitions.go` 54-56). J4 (owner call) |
| `offer-not-quotable` | RETRYABLE | 422 | the offers come from the cart's selections, not from the body, and delivery-promise answering 404 is read as "not quotable" (`open.go` 152, `deliverypromise/gateway.go` 45-46): the identical body can pass after the shopper refreshes the delivery quote. J5 |
| `validation-failed` | RETRYABLE | 422 | MIXED: the request's own fields (`handlers.go` 38-41, `transitions.go` 51), a quote that is already expired (`open.go` 100) and a cart shopping-cart does not know yet (`ports.ErrCartNotFound`, `shoppingcart/gateway.go` 71). One class per slug. J2 |
| `price-quote-rejected` | RETRYABLE | 422 | pricing's 4xx: an unpriced item or a request the checkout got wrong (`pricing/gateway.go` 70, 92-93); pricing data changes. J6 |
| `upstream-contract-violation` | RETRYABLE | 502 | a sibling answer the checkout cannot use (`allocate.go`, `open_session.go` 57-59); a 5xx is never stored |
| `shopping-cart-unavailable` | RETRYABLE | 503 | a 5xx is never stored |
| `delivery-promise-unavailable` | RETRYABLE | 503 | a 5xx is never stored |
| `pricing-unavailable` | RETRYABLE | 503 | a 5xx is never stored |
| `customer-payments-unavailable` | RETRYABLE | 503 | a 5xx is never stored |
| `customer-ordering-unavailable` | RETRYABLE | 503 | a 5xx is never stored |
| `concurrent-update` | RETRYABLE | 409 | the optimistic lock was lost after the second attempt (`deps.go` `transact`); nothing committed |
| `illegal-transition` | RETRYABLE | 409 | the aggregate has not reached the state the command needs, e.g. a confirm on a session still AUTHORIZING after an earlier 5xx (`transitions.go` 33, 45); `ResolveStuckAuthorizations` moves it on. J7 |
| `internal-error` | RETRYABLE | 500 | a 5xx is never stored |
| `dependency-unavailable` | RETRYABLE | 503 | the `/readyz` answer, no keyed route; a 5xx is never stored |

Only `authorization-mismatch` is an OUTCOME (a test pins that there is exactly one). Routes: `POST
/checkout-sessions` (open) and `POST /checkout-sessions/{sessionId}/confirmations`. MCP and A2A are read-only
(ADR 0005, ADR 0007), so no other surface stores answers.

### Judgement calls

- **J1 `not-found` RETRYABLE.** Sessions are minted here and never deleted, so the confirm 404 alone would be
  FINAL; the slug is also the answer for an over-long path identifier and is shared with ids another request
  or replica creates. Mixed slugs are RETRYABLE.
- **J2 `validation-failed` RETRYABLE.** Most causes are the request's own fields, but the slug also carries an
  unknown cart and an expired quote (state). Plain field validation is therefore no longer replayed: the retry
  costs one execution. Splitting the slug (a problem type for the unknown cart) is a recorded follow-up and a
  contract change for the owner, not part of this decision.
- **J3 `session-closed` FINAL.** A closed session never changes, like delivery-promise's `promise-closed`.
- **J4 `price-quote-expired` FINAL (owner call).** The expiry is immutable per session and monotone in time, so
  the identical retry can never pass. The question is what the shopper does next: a requote is a new
  `POST /checkout-sessions` (a different route, so necessarily another request), and the storefront BFF mints "a
  fresh key per user submit, reused on retry" (`storefront-bff` `internal/adapters/inbound/http/idempotency.go`
  `requireKey`). With a stored refusal a client that reused the key for the new request would get 409
  `idempotency-key-reused`; with a released key it would be a new request. Nothing here reuses keys, so FINAL
  keeps the refusal cheap; flipping it to RETRYABLE costs one extra execution of a pure refusal and no
  behaviour a caller can see except that reuse becomes tolerated.
- **J5 `offer-not-quotable` RETRYABLE.** Monotone in time for an offer, but the offers are read from the cart
  at request time (the body names only the cart) and an unknown offer reads as not quotable: after the shopper
  refreshes the delivery quote the byte-identical body can succeed, and a stored refusal would pin it.
- **J6 `price-quote-rejected` RETRYABLE.** Any pricing 4xx maps here (an unpriced item, or a request the
  checkout got wrong); the first can pass once pricing is fed.
- **J7 `illegal-transition` RETRYABLE.** State dependent; reached on the confirm route by a session that is
  still AUTHORIZING (an earlier attempt ended 5xx).
- **J8 the three `idempotency-key-*` types and `resource-not-found` are FINAL** although the middleware
  answers them itself and never stores them: the class records that they are a function of the request.

### What the OUTCOME test proves

`TestTheAuthorizationMismatchOutcomeIsCommittedAndReplayed` (real router, use cases, in-memory store) and
`TestReplayClassesOverRealPostgres` (real Postgres): after the refusal the session is DECLINED, the void was
made and `CheckoutDeclined` is in the outbox; the answer is stored; a retry under the key replays it with
`Idempotency-Replayed: true` and nothing is authorized, voided or published again.

## Consequences

- A refusal marked RETRYABLE is no longer replayed: a retry under the same key executes again, and a different
  body under that key is a new request. `apis/openapi.yaml` says so.
- ADR 0009's sentence "a 2xx or 4xx answer is stored" is superseded by the table above (dated pointer added
  there; its Decision text is untouched).
- **Callers.** This service also calls others with deterministic keys (customer-ordering `POST /checkout` keyed
  by the sessionId, customer-payments `<sessionId>:auth:<attempt>` and `<sessionId>:void`, pricing
  `<Idempotency-Key>:price-quote`). That is the caller side: out of scope here, no consumer-side retry is added.
  A released refusal means the same key may be tried again by whoever sent it, nothing more; a person-driven
  caller surfaces the refusal.
- Follow-ups: split `validation-failed` and `not-found` into request-only and state-dependent problem types;
  the cross-replica race (both execute, first `Put` wins) is pre-existing.
