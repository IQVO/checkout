package customerordering

// The anti-corruption layer's reading of customer-ordering's refusals on
// POST /checkout (ADR 0011).
//
// customer-ordering adopted the fleet idempotency rule (its ADR 0019, PR #91):
// every problem type of an idempotent route has a replay class. A RETRYABLE
// refusal is released: nothing was committed, the Idempotency-Key (the session
// id here) is free, and the same request under the SAME key executes again and
// may pass. A FINAL or OUTCOME refusal is stored and replayed, so sending it
// again only repeats it. That knowledge is the callee's and lives here, one
// visible table derived from the callee's merged class table; the use cases only
// see exchange.PlaceOutcome.Released.
//
// A slug that is not in the table is TERMINAL: an unknown refusal never loops.
// "Terminal" means the refusal keeps the handling it had before the rule (a
// definitive requote or rejection ends the session, an unknown outcome stays
// in doubt); only a RETRYABLE slug changes how the session reacts.
// refusal_spec_test.go holds the table against the byte-pinned callee OpenAPI
// and refusal_classes_test.go against a copy of the callee's ADR class table.

// refusalClass is how this context treats one problem slug of customer-ordering.
type refusalClass uint8

const (
	// classUnset is the zero value: the slug is not in the table, which reads as terminal.
	classUnset refusalClass = iota
	// classTerminal is a FINAL or OUTCOME refusal of the callee (stored, replayed).
	classTerminal
	// classRetryable is a RETRYABLE refusal of the callee (released under the key).
	classRetryable
)

// refusalTable maps customer-ordering's problem slug to its class here.
type refusalTable map[string]refusalClass

// retryable reports whether the callee releases the slug under the key.
func (t refusalTable) retryable(slug string) bool { return t[slug] == classRetryable }

// placeOrderRefusals follows customer-ordering ADR 0019 (POST /checkout, key =
// the session id).
var placeOrderRefusals = refusalTable{
	// RETRYABLE: nothing committed and the answer depends on state that can change.
	"not-found":                    classRetryable,
	"concurrent-update":            classRetryable,
	"illegal-transition":           classRetryable,
	"cancel-after-lock":            classRetryable,
	"validation-failed":            classRetryable, // mixed slug: an unregistered shopper or an unsaved address is a fact another request creates
	"promise-capacity-exhausted":   classRetryable, // capacity frees up
	"book-closed":                  classRetryable, // a book can reopen
	"group-spans-nodes":            classRetryable,
	"promise-destination-mismatch": classRetryable,
	"delivery-promise-unavailable": classRetryable,
	// OUTCOME: the compensation rows commit with the stored answer; the shopper re-quotes under a new key.
	"offer-not-quotable": classTerminal,
	// FINAL: a function of the request, or of a fact that never reverts.
	"malformed-request":       classTerminal,
	"line-without-promise":    classTerminal,
	"status-regression":       classTerminal,
	"shopper-exists":          classTerminal,
	"too-many-addresses":      classTerminal,
	"invalid-limit":           classTerminal, // GET only, listed so that the table names every catalogue entry
	"invalid-cursor":          classTerminal,
	"unknown-query-parameter": classTerminal,
	// the key problems: this context sends a valid key and the same body every time.
	"idempotency-key-required": classTerminal,
	"idempotency-key-invalid":  classTerminal,
	"idempotency-key-reused":   classTerminal,
}
