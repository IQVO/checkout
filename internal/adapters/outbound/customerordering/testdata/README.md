# customer-ordering contract data

`customer-ordering.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/customer-ordering
`develop` (the producer's own file). It is listed in `contracts/pinned.json`, and the CI job
`pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when it differs from the
producer's `develop`. Refresh = copy the producer's file over it, never edit it, then fix what the
contract tests report. Only `POST /checkout` (operation `placeOrder`, Idempotency-Key = sessionId)
is called by checkout.

`order.example.json` satisfies the `CustomerOrder` schema of that copy and
`conflict-409.example.json` its `CheckoutConflictProblem` (an allOf of `Problem` — which, unlike
customer-payments' Problem, also REQUIRES `detail` and `instance`). The spec carries property-level
examples only, no whole-response example for this route: the fixtures are derived from the schemas
— producer example backfill pending. `TestExamplesSatisfyPinnedSpec` checks them against the copy;
`TestBuiltRequestSatisfiesPinnedSpec` validates the body the ACL builds against
`CheckoutRequest`; `TestGatewayDecodesSpecExamples` feeds both through the real gateway call (the
conflict's first re-quoted offer names America/Chicago, the second omits timeZone and reads as
UTC). Refresh together when the network contract changes.
