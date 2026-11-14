# pricing contract data

`pricing.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/pricing `develop` (the
producer's own file). It is listed in `contracts/pinned.json`, and the CI job `pinned-contracts`
(`scripts/harness/pinned_contract_gate.py`) fails when it differs from the producer's `develop`.
Refresh = copy the producer's file over it, never edit it, then fix what the contract tests report.
Only `POST /price-quotes` (operation `issuePriceQuote`) is called by checkout, at session open.

`quote.example.json` is a response that satisfies the `PriceQuote` schema of that copy. The pricing
spec carries no examples at all: the fixture is derived from the schema (all required keys,
realistic values) — producer example backfill pending. `TestExamplesSatisfyPinnedSpec` checks it
against the copy, `TestGatewayDecodesSpecExamples` feeds it through the real gateway call and also
validates the request the gateway sends against the `IssuePriceQuoteRequest` schema. Refresh
together when the network contract changes.
