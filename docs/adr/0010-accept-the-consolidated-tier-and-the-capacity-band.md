# ADR 0010: Accept delivery-promise's CONSOLIDATED tier and carry the capacity band

## Status

Accepted (2026-11-18). Additive. Pairs with shopping-cart's ADR 0010. Records one open
decision (pricing, below).

## Context

delivery-promise defines an opt-in fifth tier, `CONSOLIDATED` (its ADR 0024): a
customer-chosen delivery day. An offer of that tier carries `consolidationEligible`, and
any offer may carry a `capacityBand` (`AMPLE`, `TIGHT`, `SCARCE`; its ADR 0023).
customer-ordering already accepts the tier (its ADR 0013). Checkout did not:
`checkoutsession.Tier.Valid()` and the OpenAPI/AsyncAPI `Tier` enums had four values, so
`numberGroups` answered `group %d is malformed` (an `ErrContractViolation`, 502
`upstream-contract-violation`) for a selection of that tier and no session could open.

## Decision

- **`CONSOLIDATED` is a session tier** (`TierConsolidated`) in the domain, the REST
  `Tier` enum and the event `Tier` enum (the `CheckoutCompleted` groups and the
  `CheckoutRequoteRequired` offers carry it). `Valid()` stays strict: any other value is
  still a contract violation, and a requoted offer of an unknown tier still fails.
- **The session group carries two optional fields** of the offer the cart selected,
  `consolidationEligible` (only with `CONSOLIDATED`) and `capacityBand`, through the
  session, its stored JSON, REST, MCP and A2A. A group without them is byte-identical to
  before. A cart group that breaks the two rules is a contract violation.
- **They are information.** The place-order request is exactly what the pinned
  customer-ordering `CheckoutGroup` names: `groupNo`, `offerId`, `lineNos`.
  customer-ordering derives the tier and the chosen delivery day from the offer (its
  `chosenDeliveryDay` and `consolidationEligible` are fields of ITS response, not of its
  request), so nothing is sent for them and nothing is invented. The pricing request
  (tier, offerId, lineNos) and the payments allocation never see the two fields.
- **No money here.** The band changes no amount; the allocation is unchanged.

## Open decision: pricing does not name CONSOLIDATED

`POST /price-quotes` (pricing's pinned `develop` contract) takes `tier` from four values
and its charge schedule names a charge for each of them. Checkout forwards the group's
tier as it is and invents no mapping to another tier, so opening a session whose selected
offer is `CONSOLIDATED` reaches pricing with a tier its contract does not list. What
pricing answers is outside this repo (a 4xx is `ErrQuoteRejected`, a 5xx
`ErrPricingUnavailable`); the session opens only once pricing accepts the tier, and which
charge `CONSOLIDATED` takes is pricing's decision.

## Follow-up

The gateway reads the tier of the cart's selected offer but not yet its two optional
fields: the pinned shopping-cart contract does not declare them until shopping-cart's
ADR 0010 is on `develop` and this repo re-pins it. The re-pin and the two DTO fields
(`consolidationEligible`, `capacityBand` on the cart gateway's offer) are the follow-up.

## Consequences

- REST: the response `Tier` enum gains a value (a `response-property-enum-value-added`
  warning in the `spec-breaking` gate) and `SessionGroup` gains two optional properties.
- Events: the `Tier` enum of the event payloads gains a value (additive for the event gate).
- Stored sessions need no migration: the two JSON keys are optional.
