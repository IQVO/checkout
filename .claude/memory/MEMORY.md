# Agent memory (harness-template v4)

Durable, repo-specific lessons that are true independent of any single incident: no fitness test needed,
just a heuristic worth an agent reading mid-task instead of re-discovering it. This is the "memory"
component the v3 harness was missing (see HARNESS.md's component table and the Harness² comparison) —
distinct from INCIDENTS.md (one row per real incident, tied to a fitness test that now guards it) and from
EVALS.md (measured claims about whether guides/sensors change agent behaviour).

**Not template-managed after first instantiation.** `tools/migrate_v4.py` creates this file only if it does
not already exist; once a repo has one, re-running migration never touches it. It is this repo's own file —
add to it when you learn something true and reusable; do not wait for an incident to justify the entry.

Format: one entry per lesson, stated as a fact with its evidence, not as an instruction to yourself.

---

- Mutation-coverage pitfall: a tagless `switch { case ...: ... }` can under-report gremlins mutator coverage
  versus the equivalent `if / if / return` chain, even with identical `go test -cover` branch coverage.
  Confirmed on warehouse-ops-agent PR #58: a pure threshold-classifier measured 89.91% mutator coverage as a
  switch (below a 96% gate), then 97.17-97.25% after rewriting as an if-chain with zero test changes. If a
  pure classification function's mutator coverage comes in surprisingly low despite full branch coverage,
  try the if-chain rewrite before assuming tests are missing a case.
- An outbound sibling call must never run inside a database transaction in the saga: the use cases save the state they
  act from (AUTHORIZING, PLACING with the stored request), call out, then save the outcome. A crash in between leaves a
  row a policy can find (`ResolveStuckAuthorizations`, `ResolveInDoubtPlacements`); a call inside the transaction would
  hold a row lock across a network timeout and lose the in-doubt marker on rollback.
- A resolver that races the request is normal: after any `Record*` conflict (`ErrSessionClosed`, `ErrIllegalTransition`,
  `ErrConcurrentUpdate`) the use case re-reads the session and answers with it instead of failing (`superseded`).
