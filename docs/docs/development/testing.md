---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
description: The test pyramid of Checkout as it is - unit, integration, godog features, contract, mutation and architecture fitness tests - with the exact make targets and the CI jobs.
---

# Testing

Every CI sensor is a `make` target, so the feedback CI gives after a push is
available before the commit. `make help` lists them all.

## The pyramid as it is

| Layer | What | Where | Run with |
| --- | --- | --- | --- |
| Unit | domain aggregate and rules, use cases over in-memory doubles, adapters over `httptest`, the composition roots | `internal/domain/**`, `internal/application/usecases`, `internal/adapters/**`, `cmd/*` (`*_test.go` without a build tag) | `make test` (`go test ./... -race`) |
| Coverage gate | statement coverage of domain and application | `COVERAGE_PKGS = ./internal/domain/...,./internal/application/...` | `make coverage`, gate 90 % |
| Godog features | Gherkin acceptance over the real chi router and use cases wired to the in-memory doubles | `features/*.feature` (5 files, 34 scenarios) with the runner and steps in `internal/bdd` | `make bdd` (`go test ./... -run TestFeatures -v`) |
| Contract | the repository contract suite run by both persistence adapters; the golden CloudEvents; the event catalogue against `apis/asyncapi.yaml`; the specs linted by Spectral | `internal/adapters/outbound/repocontract`, `internal/adapters/outbound/kafka/testdata/*.golden.json`, `internal/architecture/catalogue_fitness_test.go`, `.spectral.yaml`, `.spectral.asyncapi.yaml` | part of `make test`, `make integration`, `make arch-test`; Spectral in the `api-lint` CI job |
| Integration | real Postgres and Kafka via testcontainers; each test boots its own container | files with `//go:build integration`: `cmd/api/e2e_*_integration_test.go` (3), `internal/adapters/outbound/postgres/*_integration_test.go` (4), `internal/adapters/outbound/outbox/main_integration_test.go` | `make integration` (`go test -tags=integration ./... -race -count=1`), needs only Docker |
| Mutation | gremlins over the domain packages | `.gremlins.yaml`: efficacy threshold 95, mutant coverage threshold 90, 1 worker, timeout coefficient 30 | `make mutation` (every `internal/domain/<pkg>` in turn), `make mutation-full` (`./internal/domain`) |
| Architecture fitness | executable rules on the package graph and on source conventions | `internal/architecture` (see below) | `make arch-test` |

There are about 80 `_test.go` files; none needs a database or a broker unless
it carries the `integration` tag. The in-memory adapters
(`internal/adapters/outbound/memory`) are test doubles only: the fitness test
`TestRuntimeBinaryHasNoInMemoryAdapters` fails the build if any `cmd/**` binary
imports them.

### Godog features

| File | Scenarios | Covers |
| --- | --- | --- |
| `open_session.feature` | 8 | a ready cart opens with the binding total; an unready cart is refused before anyone else is called; an offer that expires within the margin or is no longer quoted changes nothing; an unknown cart and a missing reference are validation errors; a sibling that is down is a 503 and nothing is stored; groups that do not cover the lines are a contract bug |
| `confirm_session.feature` | 7 | authorize then place completes; a decline keeps the session open; an expired quote is refused; a closed session never changes; an authorized amount that is not the quote total is voided; a missing token is a validation error; an unknown session is not found |
| `placement.feature` | 6 | a timeout leaves the session `PLACING` and answers 202; no new attempt while unknown; the poll until the resolver settles it; a definitive 409 voids and asks for a requote; a 422 voids, fails and alerts; a failed void stays owed and is retried |
| `policies.feature` | 7 | expiry of `OPEN` and `DECLINED` sessions with the quote; an expired session cannot be confirmed; a stuck authorization is re-called with the same key; the 20 h alert; the 22 h give-up and void; the 24 h purge of the stored request |
| `api_idempotency.feature` | 6 | a write without a key is refused; a replayed open and a replayed confirm return the stored answer; the same key with another request is refused; a malformed body is a problem document; an unknown session is not found |

The scenarios run through HTTP (`httptest.Server` over `NewRouter`) so status
codes and problem slugs are asserted as a client sees them.

### Fitness tests (`internal/architecture`)

`make arch-test` runs `go test ./internal/architecture/... -v`:

- `TestHexagonalArchitecture` (arch-go): the domain imports no outer layer, an
  adapter does not reach sideways into a sibling.
- `TestRuntimeBinaryHasNoInMemoryAdapters`, `TestNoGoFilesAtRepoRoot`.
- `TestMCPAdapterDependencyRule`, `TestMCPOptionalToolInputsCarryOmitempty`,
  `TestA2AAdapterDependencyRule`, `TestA2AAdapterIsReadOnlyAndHasNoOutboundClient`.
- `TestNoAuthMiddlewareReintroduced`.
- `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope`,
  `TestEventCatalogueMatchesContract` (the published types against
  `apis/asyncapi.yaml`).
- `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice`,
  `TestDeadLetterCallsGuardCancellation`, `TestReplayConsumersSetCommitInterval`,
  `TestKafkaConsumerGroupNeverHardcodedInline`.
- `TestOutboxRelaysPassAttemptCap`.
- `TestPostgresIntegrationTestsUseTestcontainers`,
  `TestKafkaIntegrationTestsUseTestcontainers`.

Each rule has a "sensor bites" companion test that feeds it a bad fixture, so a
rule cannot pass vacuously.

## Make targets

```bash
make help          # list the targets
make build         # go build ./...
make vet           # go vet ./...
make fmt           # gofmt -w .
make fmt-check     # fail if gofmt -l . is non-empty
make lint          # golangci-lint run (v2.14.0)
make test          # go test ./... -race
make coverage      # tests + the 90% gate over domain and application
make bdd           # godog scenarios
make arch-test     # fitness tests
make integration   # testcontainers Postgres and Kafka; needs Docker
make mutation      # gremlins, fast blocking gate, every domain package
make mutation-full # gremlins over ./internal/domain (exhaustive, slow)
make vuln          # govulncheck ./...
make check-fast    # fmt-check vet arch-test + tests of changed packages
make check         # fmt-check vet build lint test
make check-all     # check + coverage arch-test bdd
make guide-lint    # agent guides: skills load, references resolve
make harness-test  # unit-test the agent hooks
```

`integration` and `mutation` are deliberately not part of `check` or
`check-all`. Git hooks (lefthook): pre-commit runs `fmt-check`, `vet` and
`lint`; pre-push runs `make check`. Activate once per clone with
`lefthook install`.

## CI jobs

`.github/workflows/ci.yml` runs on every push to `main` and `develop`, every
pull request into them, a weekly schedule (Monday 06:00 UTC, mutation only) and
manual dispatch:

| Job | What it runs |
| --- | --- |
| `lint` | golangci-lint `v2.14.0` |
| `guide-lint` | agent-guide lint and hook tests (advisory, `continue-on-error`) |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif via golangci-lint, plus an informational gocyclo report |
| `test` | `go build`, `go vet`, then `make coverage` (the 90 % gate) |
| `bdd` | `go test ./... -run TestFeatures -v` |
| `integration` | `go test -tags=integration ./... -race -count=1` |
| `mutation-fast` | gremlins over every `internal/domain/*/` package, blocking |
| `mutation` | gremlins over `./internal/domain`, only on schedule or dispatch; opens or refreshes a `harness:red` issue on a scheduled failure and closes it on recovery |
| `api-lint` | Spectral over `apis/openapi.yaml` and `apis/asyncapi.yaml`, `--fail-severity=warn` |
| `vuln` | `govulncheck ./...` |
| `helm-lint` | `ct lint` and `helm lint` with both `charts/checkout/ci` values files |
| `chart-tests` | every `charts/checkout/tests/*.sh` and `test_*.py`; a test that prints `SKIP` fails the job |
| `arch-test` | `go test ./internal/architecture/... -v` |
| `docker-publish` | main only, after the jobs above: build, push to GHCR, cosign sign, SBOM attest |
| `release` | main only, after `docker-publish`: version tag, image re-tag, chart push, GitHub release |

This documentation site has its own workflow, `.github/workflows/docs.yml`: it
builds the site on every pull request and deploys it to GitHub Pages on a push
to `main`.

## Writing a test

- A new REST error: add the sentinel to the problem catalogue
  (`internal/adapters/inbound/http/errors.go`), a case in `errors_test.go` and
  the slug to `apis/openapi.yaml`.
- A new domain rule: a table test in `internal/domain/checkoutsession` first;
  the mutation gate fails if the assertion is weak.
- A new published event: the encoder, a golden file in
  `internal/adapters/outbound/kafka/testdata`, the message in
  `apis/asyncapi.yaml`; `TestEventCatalogueMatchesContract` fails until both
  agree.
- Integration tests never read `DATABASE_URL` and never skip on a missing
  broker: they start testcontainers themselves.
