---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 2
description: Every application use case of Checkout with its trigger, inputs, invariants and the events it raises, including the five saga policies.
---

# Use cases

The use cases are the structs of `internal/application/usecases`. Every state
change on a session follows one shape inside one unit of work: load the
aggregate with its version, call the domain, save it conditional on the
version and append the events it raised to the outbox. A version conflict is
retried once, then surfaces as `concurrent-update`. **A call to a sibling is
never inside the transaction**: the saga saves the state it is about to act
from, calls out, then saves the outcome (`internal/application/usecases/deps.go`).

Errors leave as `application/problem+json` with `type` `/problems/<slug>`
(the catalogue is in `internal/adapters/inbound/http/errors.go`). The state
machine behind the use cases is on the
[Aggregate design canvas](/docs/ddd/aggregate-design-canvas).

## At a glance

| Use case | Trigger | Inbound adapter | Raises |
| --- | --- | --- | --- |
| `OpenSession` | the shopper, through the BFF | REST `POST /checkout-sessions` | `CheckoutOpened` |
| `ConfirmSession` | the shopper, through the BFF | REST `POST /checkout-sessions/{sessionId}/confirmations` | the event of the outcome (below) |
| `GetSession` | anyone | REST `GET /checkout-sessions/{sessionId}`, MCP `get_checkout_session`, A2A `checkout_session_status` | none |
| `ResolveInDoubtPlacements` | clock, 15 s | scheduler | as the settlement it ends in |
| `ResolveStuckAuthorizations` | clock, 30 s | scheduler | as the settlement it ends in |
| `RetryOwedVoids` | clock, 1 m | scheduler | none |
| `ExpireOnQuoteExpiry` | clock, 1 m | scheduler | `CheckoutSessionExpired` |
| `PurgeStoredPlacements` | clock, 1 h | scheduler | none |

The aggregate commands `RecordAuthorization`, `RecordDecline`,
`RecordPlacementInDoubt`, `RecordPlaced`, `RecordRequoteRequired`,
`RecordFailure`, `ExpireSession`, `VoidCompleted`, `MarkInDoubtAlerted` and
`PurgePlacement` are applied by the use cases after an outbound call has
answered; they have no route of their own and are described as steps below.

## OpenSession

- **Trigger:** `POST /checkout-sessions`, header `Idempotency-Key` (1 to 128
  characters) required.
- **Input:** `cartId`, `shopperId`, `shipToAddressId` (a reference to a saved
  address held by customer-ordering, never the address). All three are required
  and must be valid ids.
- **Flow:**
  1. `GetCart` on shopping-cart. A 404 is `validation-failed` (422); an
     unreachable or erroring shopping-cart is `shopping-cart-unavailable` (503).
  2. Fail fast: a cart that is not `OPEN` or whose `checkoutReadiness` is false
     is `cart-not-checkout-ready` (409) before anyone else is called.
  3. `GetOffer` on delivery-promise for each selected offer. A 404 means not
     quotable; unreachable is `delivery-promise-unavailable` (503).
  4. `IssueQuote` on pricing with the idempotency key `<Idempotency-Key>:price-quote`,
     so a retry of the open gets the same quote. A 4xx is
     `price-quote-rejected` (422); unreachable or 5xx is `pricing-unavailable` (503).
  5. `checkoutsession.Open`: validate, number the lines, build the groups,
     allocate the total, persist the session `OPEN` and write `CheckoutOpened`
     in one transaction.
- **Invariants** (`internal/domain/checkoutsession/open.go`, `allocate.go`):
  - the identifiers are valid, the cart version is at least 1;
  - 1 to 100 lines, quantity 1 to 99, lines numbered `1..n` in cart order;
  - the groups partition the lines: group numbers strictly ascending, every
    line in exactly one group, a valid offer and tier on each; otherwise
    `ErrContractViolation` (`upstream-contract-violation`, 502, with the alert
    `sibling-contract-violation`);
  - every selected offer is `QUOTED` and expires more than 2 minutes from now,
    otherwise `offer-not-quotable` (422) and no state changes;
  - the quote prices every line once and has one non-negative charge per group;
    each group's amount is its item totals + its delivery charge + a pro-rata
    share of the tax (the remainder goes to the first group) and the amounts sum
    to the quote total exactly, otherwise `ErrContractViolation`;
  - the quote must not already be expired.
- **Raises:** `CheckoutOpened` (analytics).
- **Answer:** 201 with the session, its binding total and `quoteExpiresAt`.

## ConfirmSession

- **Trigger:** `POST /checkout-sessions/{sessionId}/confirmations`, `Idempotency-Key`
  required.
- **Input:** `paymentMethodToken` (the PSP token, never a card number; non-blank,
  at most 512 characters).
- **Flow:**
  1. `Confirm`: `OPEN` or `DECLINED` becomes `AUTHORIZING`; the token is kept
     only while `AUTHORIZING`; from `DECLINED` the attempt counter grows by one.
     Saved before the call out.
  2. `Authorize` on customer-payments with the key `<sessionId>:auth:<attempt>`,
     the quote total and one allocation per group. A decline (`DECLINED` state,
     or a 422 from payments) becomes `RecordDecline`. An unknown outcome
     (transport error, timeout, a 409 or a 5xx) leaves the session `AUTHORIZING`
     and the call answers 202.
  3. `RecordAuthorization`: `AUTHORIZING` becomes `PLACING` and the PII-free
     placement request is stored in the same save.
  4. `Place` on customer-ordering (`POST /checkout`, `Idempotency-Key` equal to
     the sessionId, 15 s timeout).
  5. Settle the answer (below). For a requote or a rejection the void of the
     authorization is attempted at once.
- **Invariants** (`internal/domain/checkoutsession/transitions.go`):
  - a session in a closed state is `session-closed` (409); `PLACING` is
    `placement-in-doubt` (409, the semantic lock); `AUTHORIZING` is
    `illegal-transition` (409);
  - a session with a void still owed is `void-pending` (409);
  - the quote must not be expired: `price-quote-expired` (422);
  - the authorized amount and currency must equal the quote's, otherwise the
    session returns to `DECLINED` with the void owed, `CheckoutDeclined` is
    raised with reason `authorization-mismatch`, the alert fires and the call
    answers 409 `authorization-mismatch`;
  - the stored placement request is non-empty and hashed (SHA-256).
- **Settlement of the place-order answer** (`internal/application/usecases/settle.go`):

| customer-ordering answers | Result | State | HTTP | Event |
| --- | --- | --- | --- | --- |
| 2xx with an `orderId` | `RecordPlaced` | `COMPLETED` | 200 | `CheckoutCompleted` |
| 409 `offer-not-quotable`, `promise-capacity-exhausted` or `book-closed` | `RecordRequoteRequired` (offers formatted in the destination's time zone), void owed | `REQUOTE_REQUIRED` | 200 | `CheckoutRequoteRequired` |
| 400 or 422 | `RecordFailure` code `place-order-rejected`, void owed, alert `place-order-rejected` | `FAILED` | 200 | `CheckoutFailed` |
| timeout, transport error, 5xx, a retriable 409, an answer without an `orderId` | `RecordPlacementInDoubt` | `PLACING` | 202 | `CheckoutPlacementInDoubt` (first time only) |

- **Idempotency:** the `Idempotency-Key` middleware replays the stored first
  response; downstream keys are deterministic: `<sessionId>:auth:<attempt>`,
  the sessionId for the place-order call and `<sessionId>:void`. A confirm after
  a decline is a new attempt (counter plus one), so it is not answered with the
  first declined response, while a stuck call replays the same key.
- The shopper-visible metric of every attempt is `checkout.sessions.confirmed`
  (see [Observability](/docs/operations/observability#metrics)).

## GetSession

`GET /checkout-sessions/{sessionId}` returns the session or `not-found` (404).
It is the polling endpoint for a `PLACING` or `AUTHORIZING` session. The same
use case serves `cmd/mcp` and `cmd/a2a`. A session id longer than 128
characters can never name a session and is `not-found` on all three surfaces.

## The saga policies

All five run in `cmd/api`, each on its own tick, drain their backlog in batches
of 200 and skip a session another actor already moved. A failing tick is logged
and retried at the next one.

### ResolveInDoubtPlacements

- **Finds:** `PLACING` sessions whose `next_attempt_at` is due.
- **Does:** re-sends the stored body to customer-ordering with the SAME
  `Idempotency-Key` (the sessionId), so customer-ordering replays its first
  answer and a pass that raced a slow first call cannot place a second order,
  then settles the answer as in `ConfirmSession`. Each pass without a definitive
  answer pushes `next_attempt_at` out by an exponential backoff from 30 s,
  doubling to 15 minutes.
- **Timeline** (from `placing_since`): alert `placement-in-doubt-20h` once at
  20 h; at 22 h, if the place-order call still fails, the session becomes
  `FAILED` with code `placement-unresolved`, the alert `placement-given-up-22h`
  fires and the authorization is voided. Both sit inside customer-payments' 24 h
  backstop and the network's 24 h idempotency window; the void is idempotent so a
  race with that backstop is harmless.
- **Raises:** the settlement's event (`CheckoutCompleted`,
  `CheckoutRequoteRequired`, `CheckoutFailed`).

### ResolveStuckAuthorizations

- **Finds:** `AUTHORIZING` sessions older than 2 minutes (a crash or timeout
  between the save and the answer).
- **Does:** re-calls `Authorize` with the SAME key `<sessionId>:auth:<attempt>`
  and the kept token, which is idempotent and yields the true outcome, then
  continues as in `ConfirmSession` (`RecordAuthorization` or `RecordDecline`).

### RetryOwedVoids

- **Finds:** sessions with an owed void.
- **Does:** voids the authorization (`<sessionId>:void`) and clears the debt. A
  404 or a 409 `illegal-transition` from customer-payments counts as done. A
  failed void is logged (`void failed, it stays owed`) and retried at the next
  tick. Covers the mismatch void and every definitive-rejection void.

### ExpireOnQuoteExpiry

- **Finds:** `OPEN` or `DECLINED` sessions whose `quote_expires_at` has passed.
- **Does:** `ExpireSession` moves them to `EXPIRED` (refused with `validation-failed`
  if the quote has not expired). **Raises** `CheckoutSessionExpired` (analytics).

### PurgeStoredPlacements

- **Finds:** closed sessions 24 h after `terminal_at` that still hold a stored
  placement request.
- **Does:** drops the stored placement request (`placement_body`,
  `placement_hash` and the `placing_since` it carries): the body is kept only as
  long as a replay can still be needed. Raises no event.

## Events by outcome

| Event | Raised by | Topic |
| --- | --- | --- |
| `CheckoutOpened` | `OpenSession` | analytics |
| `CheckoutDeclined` | `RecordDecline` (payment declined) and the authorization mismatch | analytics |
| `CheckoutPlacementInDoubt` | the first in-doubt placement | analytics |
| `CheckoutCompleted` | `RecordPlaced` | events |
| `CheckoutRequoteRequired` | `RecordRequoteRequired` | analytics |
| `CheckoutFailed` | `RecordFailure` | analytics |
| `CheckoutSessionExpired` | `ExpireSession` | analytics |

`Confirm` and a successful `RecordAuthorization` raise nothing. Payloads are on
the [Event catalogue](/docs/api-reference/events).
