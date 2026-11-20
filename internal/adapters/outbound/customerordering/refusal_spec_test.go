package customerordering

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The refusal table is the consumer half of the fleet idempotency rule. These
// tests keep it honest against the byte-pinned copy of customer-ordering's
// OpenAPI (contracts/pinned.json): the Idempotency-Key text of POST /checkout
// lists the problem types that are not stored (released, "retry with the same
// key") and the ones that are stored ("use a new key"), and the 4xx/503
// responses name the rest. A slug the pin names that the table leaves out, or a
// slug the pin releases that the table calls terminal, fails.

const (
	releasedAnchor = "These problem types are not stored:"
	releasedEnd    = "(retry with the same key)"
	storedAnchor   = "The others are stored:"
	storedEnd      = "(use a new key)"
)

// notProblemTypes are hyphenated words of the spec prose that are not problem types.
var notProblemTypes = map[string]bool{
	"delivery-promise": true, "customer-ordering": true, "re-quotes": true, "re-quoted": true,
	"client-generated": true, "ship-to": true,
}

var slugWord = regexp.MustCompile(`(?:^|[^/A-Za-z0-9-])([a-z]+(?:-[a-z]+)+)(?:[^/A-Za-z0-9-]|$)`)

func slugsIn(text string) []string {
	seen := map[string]bool{}
	var out []string
	for rest := text; ; {
		m := slugWord.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}
		if slug := rest[m[2]:m[3]]; !notProblemTypes[slug] && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
		rest = rest[m[3]:]
	}
	sort.Strings(out)
	return out
}

func words(s string) string { return strings.Join(strings.Fields(s), " ") }

// between returns the text from the end of anchor to the next end marker.
func between(t *testing.T, text, anchor, end string) string {
	t.Helper()
	i := strings.Index(text, anchor)
	if i < 0 {
		t.Fatalf("the Idempotency-Key text no longer has %q: customer-ordering reworded how it lists the stored and released refusals; read the new text and update the anchors and the table together", anchor)
	}
	rest := text[i+len(anchor):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("the Idempotency-Key text no longer has %q after %q", end, anchor)
	}
	return rest[:j]
}

// specSlugs reads the problem types the pinned spec names on POST /checkout:
// released are the ones it says are not stored, stored the ones it says are,
// named every slug of the key text and of the 4xx and 503 responses.
func specSlugs(t *testing.T) (released, stored, named []string) {
	t.Helper()
	op := loadPinned(t).Paths.Find("/checkout").Operations()[http.MethodPost]
	if op == nil {
		t.Fatal("the pinned spec has no POST /checkout")
	}
	var text string
	for _, p := range op.Parameters {
		if p.Value != nil && p.Value.Name == "Idempotency-Key" {
			text = words(p.Value.Description)
		}
	}
	all := map[string]bool{}
	for _, s := range slugsIn(text) {
		all[s] = true
	}
	for status, ref := range op.Responses.Map() {
		if (strings.HasPrefix(status, "4") || status == "503") && ref.Value != nil && ref.Value.Description != nil {
			for _, s := range slugsIn(words(*ref.Value.Description)) {
				all[s] = true
			}
		}
	}
	for s := range all {
		named = append(named, s)
	}
	sort.Strings(named)
	return slugsIn(between(t, text, releasedAnchor, releasedEnd)), slugsIn(between(t, text, storedAnchor, storedEnd)), named
}

// auditTable returns what is wrong between the table and the spec's reading.
func auditTable(table refusalTable, released, stored, named []string) []string {
	var problems []string
	for _, s := range released {
		if table[s] != classRetryable {
			problems = append(problems, "spec releases "+s+" under the key but the table does not map it retryable")
		}
	}
	for _, s := range stored {
		if table[s] != classTerminal {
			problems = append(problems, "spec stores "+s+" under the key but the table does not map it terminal")
		}
	}
	for _, s := range named {
		if table[s] == classUnset {
			problems = append(problems, "spec names "+s+" and the table classifies it neither retryable nor terminal")
		}
	}
	return problems
}

func TestEveryProblemTypeThePinnedSpecNamesIsClassifiedAndEveryReleasedOneRetries(t *testing.T) {
	released, stored, named := specSlugs(t)
	if len(released) != 10 || len(stored) != 6 || len(named) < 16 {
		t.Fatalf("read %d released, %d stored and %d named problem types from the pin (want 10, 6, >= 16): the extraction is broken or the callee changed its class table", len(released), len(stored), len(named))
	}
	if problems := auditTable(placeOrderRefusals, released, stored, named); len(problems) > 0 {
		t.Errorf("customer-ordering:\n  %s", strings.Join(problems, "\n  "))
	}
}

// The audit itself must fail when the table drifts.
func TestTheAuditFailsWhenTheTableDrifts(t *testing.T) {
	released, stored, named := specSlugs(t)
	drifted := refusalTable{}
	for k, v := range placeOrderRefusals {
		drifted[k] = v
	}
	delete(drifted, "line-without-promise")
	drifted["book-closed"] = classTerminal
	drifted["offer-not-quotable"] = classRetryable
	got := strings.Join(auditTable(drifted, released, stored, named), "\n")
	for _, want := range []string{"line-without-promise", "book-closed", "offer-not-quotable"} {
		if !strings.Contains(got, want) {
			t.Errorf("the audit missed %s:\n%s", want, got)
		}
	}
}
