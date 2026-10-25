# ADR 0002: Aggregate, invariants and assumptions

## Status

Accepted (2026-10-25).

## Context

The design source is warehouse-docs `storefront/model/model.yaml`, context `checkout`,
with the FINAL decisions of the storefront campaign applied on top (they override the
model where they differ): H1 DECLINED -> EXPIRED, H2 `authAttempt` and the key
`<sessionId>:auth:<attempt>`, H3 `ErrOfferNotQuotable` (422, no state change), H4 the
policy ResolveStuckAuthorizations, H5 `voidOwed` and the policy RetryOwedVoids, H6 the
ordered timeline (alert 20 h < give up and void 22 h < customer-payments backstop 24 h),
H7 the analytics events CheckoutSessionExpired and CheckoutPlacementInDoubt, H9
StoredPlacement; and contracts-delta S1 (lines numbered 1..n, a quote group that cannot
be formed from the cart is a contract bug), S2 (the binding number is the PriceQuote;
confirming after `quoteExpiresAt` is 422 `price-quote-expired`) and S3 (pre-check, then
void and REQUOTE_REQUIRED). This ADR records the aggregate as built and every
assumption (A1, A2, ...) taken where the model and the decisions are silent. Each is
also reported as a `model_gap`.

## Decision

### Aggregate

- **CheckoutSession** (`internal/domain/checkoutsession`, root `sessionId`): `cartId` and
  `cartVersion`, `shopperId`, `shipToAddressId` (references, never PII), numbered lines
  `{lineNo, catalogItemId, quantity}`, groups `{groupNo, offerId, tier, lineNos,
  amountMinor}`, `priceQuoteId`, `currency`, `totalMinor`, `quoteExpiresAt`,
  `authorizationId`, `orderId`, `requotedOffers`, `failureCode`, `authAttempt`,
  `voidOwed`, the StoredPlacement `{requestBody, requestHash, placingSince}`, `version`.
  - States OPEN, AUTHORIZING, DECLINED, PLACING, REQUOTE_REQUIRED, COMPLETED, EXPIRED,
    FAILED with exactly the model's transitions plus DECLINED -> EXPIRED (H1).
  - COMPLETED, EXPIRED, FAILED and REQUOTE_REQUIRED are closed (`ErrSessionClosed`).
  - PLACING is the semantic lock: no new attempt while the outcome is unknown
    (`ErrPlacementInDoubt`).
- Errors and problem types: `ErrCartNotCheckoutReady` 409, `ErrSessionClosed` 409,
  `ErrAuthorizationMismatch` 409, `ErrPlacementInDoubt` 409, `ErrVoidPending` 409 (A4),
  `ErrPriceQuoteExpired` 422 (A6), `ErrOfferNotQuotable` 422, `ErrContractViolation` 502
  (A8), plus the shared `not-found`, `illegal-transition`, `concurrent-update` and
  `validation-failed`.

### Assumptions

- **A1** `authAttempt` starts at 0 and grows on each DECLINED -> AUTHORIZING, so the keys
  are `<sessionId>:auth:0`, `:auth:1`, ... The pack OpenAPI text `<sessionId>:auth` is
  superseded by H2.
- **A2** The PSP token is kept on the session ONLY while it is AUTHORIZING (needed to
  re-call Authorize with the same key, H4) and cleared when the state is left. It is a
  token, never a card number, and never leaves the service in an event or a response.
- **A3** When the Authorize outcome is unknown (transport error, 5xx, 409) the confirm
  route answers 202 with the session still AUTHORIZING (the pack lists 202 for PLACING
  only); the BFF polls and ResolveStuckAuthorizations settles it after 2 minutes.
- **A4** An authorized amount or currency that is not the quote's puts the session back
  to DECLINED (reason `authorization-mismatch`, event CheckoutDeclined) with the void
  owed (H5) and answers 409 `authorization-mismatch`. No AUTHORIZING -> FAILED transition
  is invented. A new confirm is refused with 409 `void-pending` (`ErrVoidPending`) until
  the void succeeded, because customer-payments allows one active authorization per session.
- **A5** The void is always recorded as owed in the same change as the decision (REQUOTE
  _REQUIRED or FAILED), attempted right away and cleared on success; a failed void is
  retried every minute. The pack text "only after the void succeeded" is therefore read as
  "the void is guaranteed, not yet done". The give-up at 22 h follows the same path.
- **A6** `price-quote-expired` is 422 (contracts-delta S2); the pack OpenAPI said 409.
- **A7** The allocation of a group is its item totals plus its delivery charge plus a
  pro-rata share of the tax (floor, remainder to the first group), so the allocations sum
  to the quote total exactly; a quote that does not add up is a contract bug.
- **A8** A sibling answer that cannot form a session (groups that do not cover the lines,
  a quote that does not price a line) is `ErrContractViolation`: 502
  `upstream-contract-violation` and an alert. FAILED exists only after PLACING, so no
  session is created.
- **A9** GetCart, GetDeliveryOffer and IssuePriceQuote run before the aggregate checks the
  offers; a quote issued before an `offer-not-quotable` answer simply expires.
- **A10** CheckoutPlacementInDoubt is raised on the first unknown outcome only.
- **A11** The resolver backs off 30 s, doubling to 15 min. The 20 h alert fires once
  (`alertedAt`); at 22 h one last replay is tried and, if still unknown, the session fails
  with code `placement-unresolved` and the void.
- **A12** Several sessions may exist for one cart; a cart closes on CheckoutCompleted.
- **A13** The network's `deliverBy` date-times become ISO dates (the UTC date) in
  `requotedOffers`.
- **A14** A cart is ready only when its own readiness holds AND its state is OPEN.
- **A15** An unknown offer (404) is reported as not quotable, not as an error.
- **A16** The placement purge drops only the stored request, 24 h after a closed state; the
  session row stays.
- **A17** Timestamps are truncated to the microsecond (Postgres) in the domain.
