# shopping-cart contract data

`shopping-cart.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/shopping-cart `develop`
(the producer's own file). It is listed in `contracts/pinned.json`, and the CI job `pinned-contracts`
(`scripts/harness/pinned_contract_gate.py`) fails when it differs from the producer's `develop`.
Refresh = copy the producer's file over it, never edit it, then fix what the contract tests report.
Only `GET /carts/{cartId}` (operation `getCart`) is called by checkout.

`cart.example.json` is a response that satisfies the `Cart` schema of that copy (the spec carries no
inline example for this route; the fixture is derived from the schema — producer example backfill
pending). `TestExamplesSatisfyPinnedSpec` checks it against the copy, and
`TestGatewayDecodesSpecExamples` feeds it through the real gateway call. Its group names the
NEXT_DAY offer the selection picks, so the gateway's tier mapping is exercised. Refresh both
together when the network contract changes.
