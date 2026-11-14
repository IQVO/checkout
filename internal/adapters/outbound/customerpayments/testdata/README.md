# customer-payments contract data

`customer-payments.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/customer-payments
`develop` (the producer's own file). It is listed in `contracts/pinned.json`, and the CI job
`pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when it differs from the
producer's `develop`. Refresh = copy the producer's file over it, never edit it, then fix what the
contract tests report. checkout calls `POST /payment-authorizations` (operation `authorizePayment`,
with key <sessionId>:auth) and `POST /payment-authorizations/{authorizationId}/voids` (operation
`voidPaymentAuthorization`, with key <sessionId>:void).

`authorization.example.json` (AUTHORIZED) and `authorization-declined.example.json` (DECLINED,
providerAuthorizationId null, declineReason present) satisfy the `PaymentAuthorization` schema of
that copy; `decline-422.example.json` and `void-conflict.example.json` satisfy its `Problem`
schema. The customer-payments spec carries no examples at all: the fixtures are derived from the
schemas (all required keys, realistic values) — producer example backfill pending.
`TestExamplesSatisfyPinnedSpec` checks them against the copy; `TestGatewayDecodesSpecExamples` and
`TestVoidAnswerMapping` feed them through the real gateway calls (the 422 problem is the decline
reason `authorization-rejected: allocation-mismatch`; the 409 illegal-transition void is done).
Refresh together when the network contract changes.
