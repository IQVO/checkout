# ADR 0008: Requoted delivery dates in the destination's time zone

## Status

Accepted (2026-10-30).

## Context

A definitive 409 from customer-ordering's `POST /checkout` (`offer-not-quotable`,
`promise-capacity-exhausted`, `book-closed`) carries `requotedOffers`, each with a delivery
window as two instants (`deliverByEarliest`, `deliverByLatest`). The anti-corruption layer
(`internal/adapters/outbound/customerordering/gateway.go`) turns them into calendar dates
(`cs.RequotedOffer`) that checkout returns on the session, publishes on
`CheckoutRequoteRequired` and posts to shopping-cart `/carts/{id}/delivery-requote-acceptances`.

It formatted both instants in UTC. A shopper reads a window as days of the DESTINATION's
calendar: a Saturday 09:00-21:00 window in Chicago is `2026-10-31T14:00:00Z` ..
`2026-11-01T02:00:00Z`, which UTC shows as Oct 31 - Nov 1, a day the window does not
touch locally.

delivery-promise (its ADR 0011) now names the destination's IANA zone as an optional
`timeZone` on every window, and customer-ordering relays it on each requoted offer
(`requotedOffers[].timeZone`, additive). It is absent for rows stored before the change.
shopping-cart already reads quoted windows in that zone (its ADR 0007); the requote path
must agree with it, or the cart flips its dates when checkout requotes.

Weighed alternatives:

- Send the instants and the zone to shopping-cart and let it format. Rejected: the cart's
  requoted endpoint takes dates, the session and the `CheckoutRequoteRequired` event expose
  dates, and changing three contracts for a formatting rule is more than the fix needs.
- Keep UTC. Rejected: it is the bug.
- Use the server's local zone. Rejected: it is the cluster's zone, never the destination's.

## Decision

1. The gateway reads the optional `timeZone` of each requoted offer and formats both window
   bounds as `time.DateOnly` in that zone. Each offer uses its own zone.
2. A missing zone, the name `Local` (the process zone), or a name the zone database does not
   know falls back to UTC, the dates checkout sent before. Each such name is logged at WARN
   the first time the gateway sees it (`zones` in
   `internal/adapters/outbound/customerordering/zone.go`, the same shape as shopping-cart's
   resolver), so a provider that never sends the zone, or sends a bad one, is visible
   without flooding the log. At most 64 names are cached; past that a name is still
   resolved, only not cached or warned about.
3. `cmd/api/tzdata.go` embeds the zone database (`time/tzdata`), so names resolve on a base
   image without tzdata. Only `cmd/api` places orders; `cmd/mcp` and `cmd/a2a` do not need it.
4. Checkout's own contracts are unchanged in shape: `deliverByEarliest`/`deliverByLatest`
   stay `format: date`; their descriptions now say they are the destination's calendar days
   (`apis/openapi.yaml` `RequotedOffer`, `apis/asyncapi.yaml` `CheckoutRequoteRequired`).
   No new field, no new event version.

`docs/adr` has no index file (see ADR 0006), so none is edited.

## Consequences

- A requoted window that is one local day shows as one day, in the session, in the event
  and in the cart, matching what shopping-cart shows for the original quote.
- Until customer-ordering relays `timeZone`, every requote logs one WARN (the empty name)
  per process and keeps UTC dates: nothing breaks, the gap is visible.
- The binary grows by the embedded zone database (about 450 KB).
- Tests: `internal/adapters/outbound/customerordering/zone_test.go` (Chicago
  14:00Z..02:00Z is 2026-10-31 for both bounds; a missing zone reads as UTC; unusable names
  warn once; the cache is bounded).
