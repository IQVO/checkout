package customerordering

import (
	"sort"
	"testing"
)

// adrClasses is a copy of the class table of customer-ordering's ADR 0019
// ("The class of every problem type", catalogue `problemCatalogue` in its
// internal/adapters/inbound/http/errors.go, 19 entries, merged in its PR #91)
// plus the three key problems the middleware writes. "R" is RETRYABLE, "T" is
// FINAL or OUTCOME. Refresh it with the ADR, never alone.
var adrClasses = map[string]string{
	// RETRYABLE (10)
	"not-found": "R", "concurrent-update": "R", "illegal-transition": "R", "cancel-after-lock": "R",
	"validation-failed": "R", "promise-capacity-exhausted": "R", "book-closed": "R",
	"group-spans-nodes": "R", "promise-destination-mismatch": "R", "delivery-promise-unavailable": "R",
	// FINAL (8)
	"malformed-request": "T", "invalid-limit": "T", "invalid-cursor": "T", "unknown-query-parameter": "T",
	"line-without-promise": "T", "status-regression": "T", "shopper-exists": "T", "too-many-addresses": "T",
	// OUTCOME (1)
	"offer-not-quotable": "T",
	// the key problems: checkout sends a valid key and the same body every time
	"idempotency-key-required": "T", "idempotency-key-invalid": "T", "idempotency-key-reused": "T",
}

func TestTheTableEqualsTheCalleeADRClassTable(t *testing.T) {
	if len(adrClasses) != 22 {
		t.Fatalf("the ADR copy holds %d rows, want 19 catalogue entries and 3 key problems", len(adrClasses))
	}
	for slug, want := range adrClasses {
		got := placeOrderRefusals[slug]
		switch {
		case want == "R" && got != classRetryable:
			t.Errorf("%s is RETRYABLE at customer-ordering and must be retryable here", slug)
		case want == "T" && got != classTerminal:
			t.Errorf("%s is FINAL or OUTCOME at customer-ordering and must be explicitly terminal here", slug)
		}
	}
	for slug := range placeOrderRefusals {
		if _, ok := adrClasses[slug]; !ok {
			t.Errorf("%s is in the table but not in the callee's ADR class table", slug)
		}
	}
}

func TestEveryTableEntryIsExplicit(t *testing.T) {
	for slug, class := range placeOrderRefusals {
		if class != classRetryable && class != classTerminal {
			t.Errorf("%s has class %d", slug, class)
		}
	}
}

func TestUnknownAndEmptySlugsAreTerminal(t *testing.T) {
	for _, slug := range []string{"", "brand-new-problem", "/problems/book-closed", "BOOK-CLOSED"} {
		if placeOrderRefusals.retryable(slug) {
			t.Errorf("%q must not be retryable", slug)
		}
	}
	var retryable []string
	for slug := range placeOrderRefusals {
		if placeOrderRefusals.retryable(slug) {
			retryable = append(retryable, slug)
		}
	}
	sort.Strings(retryable)
	if len(retryable) != 10 {
		t.Errorf("the 10 RETRYABLE problem types of ADR 0019 are retryable, got %v", retryable)
	}
}
