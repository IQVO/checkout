# Residual mutants (gremlins v0.6.0, `make mutation`, measured 2026-11-15)

These mutants cannot be killed or covered by a test. They are not silenced in
`.gremlins.yaml`; the `checkoutsession` package carries them, which is why the
gates sit at 98/94 and not at the fleet's 99/99.

| Package | File:line | Mutator | Status | Why |
| --- | --- | --- | --- | --- |
| checkoutsession | `transitions.go:142:31` (`backoff` loop `d < MaxBackoff`) | CONDITIONALS_BOUNDARY | LIVED, equivalent | `d` takes the values 30s, 1m, 2m, 4m, 8m, 16m (`firstBackoff` doubled), never exactly `MaxBackoff` (15m), so `<` and `<=` behave the same. |
| checkoutsession | `transitions.go:145:7` (`backoff` clamp `d > MaxBackoff`) | CONDITIONALS_BOUNDARY | LIVED, equivalent | When `d == MaxBackoff` the function returns `d`, which is `MaxBackoff`: `>` and `>=` return the same value. |
| checkoutsession | `session.go:57:19` (`OfferMargin = 2 * time.Minute`) | ARITHMETIC_BASE | NOT COVERED, artefact | A `const` initialiser is evaluated at compile time; the cover profile has no statement for it. |
| checkoutsession | `session.go:58:20` (`MaxBackoff = 15 * time.Minute`) | ARITHMETIC_BASE | NOT COVERED, artefact | Same. |
| checkoutsession | `session.go:59:20` (`firstBackoff = 30 * time.Second`) | ARITHMETIC_BASE | NOT COVERED, artefact | Same. |
| checkoutsession | `transitions.go:14:30` (`StuckAuthorizationAfter = 2 * time.Minute`) | ARITHMETIC_BASE | NOT COVERED, artefact | Same. |
| checkoutsession | `transitions.go:15:31` (`InDoubtAlertAfter = 20 * time.Hour`) | ARITHMETIC_BASE | NOT COVERED, artefact | Same. |
| checkoutsession | `transitions.go:16:31` (`InDoubtGiveUpAfter = 22 * time.Hour`) | ARITHMETIC_BASE | NOT COVERED, artefact | Same. |
| checkoutsession | `transitions.go:17:31` (`PurgePlacementAfter = 24 * time.Hour`) | ARITHMETIC_BASE | NOT COVERED, artefact | Same. |

`checkIdentity` (`open.go`) used a tagless `switch { case ...: }`, which the Go
cover tool does not instrument (2 NOT COVERED mutants); it is now an if-chain
with the same behaviour.
