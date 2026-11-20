package customerordering_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
)

func problem(slug string) string {
	return fmt.Sprintf(`{"type":"/problems/%s","title":"t","status":0,"detail":"why %s"}`, slug, slug)
}

// A refusal customer-ordering releases (replay class RETRYABLE, ADR 0019) is
// flagged Released, with the reading it had before the rule (a requote for the
// two definitive 409s, a rejection for a 400/422) and its slug.
func TestAReleasedRefusalIsFlaggedAndKeepsItsTerminalReading(t *testing.T) {
	cases := []struct {
		status int
		slug   string
		kind   exchange.OutcomeKind
	}{
		{409, "promise-capacity-exhausted", exchange.OutcomeRequote},
		{409, "book-closed", exchange.OutcomeRequote},
		{422, "validation-failed", exchange.OutcomeRejected},
		{422, "group-spans-nodes", exchange.OutcomeRejected},
		{422, "promise-destination-mismatch", exchange.OutcomeRejected},
	}
	for _, c := range cases {
		g, got := serve(t, c.status, problem(c.slug))
		out, err := g.Place(context.Background(), "sess-1", []byte("{}"))
		if err != nil || out.Kind != c.kind || !out.Released || out.Slug != c.slug {
			t.Fatalf("%s: out %+v err %v", c.slug, out, err)
		}
		if got.key != "sess-1" {
			t.Fatalf("%s: key %q", c.slug, got.key)
		}
	}
}

func TestAReleasedRefusalCarriesItsStatusSlugAndDetail(t *testing.T) {
	g, _ := serve(t, 422, problem("validation-failed"))
	out, _ := g.Place(context.Background(), "s", []byte("{}"))
	if out.Detail != "422 validation-failed: why validation-failed" {
		t.Fatalf("detail %q", out.Detail)
	}
}

// FINAL and OUTCOME refusals, an unknown slug and a 4xx without a problem body
// are terminal: not flagged, handled exactly as before the rule.
func TestAFinalOrUnknownRefusalIsNotReleased(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   exchange.OutcomeKind
	}{
		{"line-without-promise (FINAL)", 422, problem("line-without-promise"), exchange.OutcomeRejected},
		{"malformed-request (FINAL)", 400, problem("malformed-request"), exchange.OutcomeRejected},
		{"unknown 422 slug", 422, problem("brand-new-problem"), exchange.OutcomeRejected},
		{"422 without a body", 422, ``, exchange.OutcomeRejected},
		{"400 without a problem body", 400, `nope`, exchange.OutcomeRejected},
		{"offer-not-quotable (OUTCOME)", 409, problem("offer-not-quotable"), exchange.OutcomeRequote},
	}
	for _, c := range cases {
		g, _ := serve(t, c.status, c.body)
		out, err := g.Place(context.Background(), "s", []byte("{}"))
		if err != nil || out.Kind != c.kind || out.Released {
			t.Fatalf("%s: out %+v err %v", c.name, out, err)
		}
	}
}

// A refusal that was already an unknown outcome stays one: the session stays
// PLACING and the resolver retries it under the same key, as before the rule.
func TestAnUnknownOutcomeStaysInDoubtWhateverItsSlug(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"concurrent-update (RETRYABLE)":            {409, problem("concurrent-update")},
		"delivery-promise-unavailable (RETRYABLE)": {503, problem("delivery-promise-unavailable")},
		"not-found (RETRYABLE)":                    {404, problem("not-found")},
		"unknown 409 slug":                         {409, problem("brand-new-problem")},
		"409 without a body":                       {409, ``},
		"idempotency-key-reused (terminal)":        {409, problem("idempotency-key-reused")},
	}
	for name, c := range cases {
		g, _ := serve(t, c.status, c.body)
		out, err := g.Place(context.Background(), "s", []byte("{}"))
		if !errors.Is(err, ports.ErrOrderingUnavailable) || out.Released {
			t.Fatalf("%s: out %+v err %v", name, out, err)
		}
	}
}
