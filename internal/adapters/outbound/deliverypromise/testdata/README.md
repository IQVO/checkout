# delivery-promise contract data

`delivery-promise.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/delivery-promise
`develop` (the producer's own file). It is listed in `contracts/pinned.json`, and the CI job
`pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when it differs from the
producer's `develop`. Refresh = copy the producer's file over it, never edit it, then fix what the
contract tests report. Only `GET /delivery-offers/{offerId}` (operation `getDeliveryOffer`) is
called by checkout (the pre-check before authorizing).

`offer.example.json` (QUOTED) and `offer-expired.example.json` (EXPIRED) satisfy the `DeliveryOffer`
schema of that copy. The spec carries property-level examples only (787, America/Chicago,
node-pnt-7), no whole-response example for this route: the fixtures are derived from the schema
using those values — producer example backfill pending. `TestExamplesSatisfyPinnedSpec` checks both
against the copy; `TestGatewayDecodesSpecExamples` feeds each through the real gateway call (QUOTED
is quotable, EXPIRED is not). Refresh together when the network contract changes.
