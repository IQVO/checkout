package checkoutsession_test

import (
	"fmt"
	"reflect"
	"testing"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// maxLinesInput is a cart of exactly cs.MaxLines lines in one group, each
// priced 10 minor units, with one delivery charge of 100.
func maxLinesInput() cs.OpenInput {
	in := validInput()
	in.Cart.Lines, in.Cart.Groups, in.Quote.Lines = nil, nil, nil
	nos := make([]int, 0, cs.MaxLines)
	for i := 1; i <= cs.MaxLines; i++ {
		id := fmt.Sprintf("ITEM-%d", i)
		in.Cart.Lines = append(in.Cart.Lines, cs.CartLine{CatalogItemID: id, Quantity: 1})
		in.Quote.Lines = append(in.Quote.Lines, cs.QuoteLine{CatalogItemID: id, LineTotalMinor: 10})
		nos = append(nos, i)
	}
	in.Cart.Groups = []cs.CartGroup{{GroupNo: 1, OfferID: "offer-1", Tier: cs.TierNextDay, LineNos: nos}}
	in.Offers = in.Offers[:1]
	in.Quote.Charges = []cs.QuoteCharge{{GroupNo: 1, ChargeMinor: 100}}
	in.Quote.TaxMinor, in.Quote.TotalMinor = 0, 1100
	return in
}

func TestOpenAcceptsExactlyMaxLines(t *testing.T) {
	s, err := cs.Open(maxLinesInput(), t0)
	wantNoErr(t, err)
	if len(s.Lines()) != cs.MaxLines || s.Lines()[cs.MaxLines-1].LineNo != cs.MaxLines || s.TotalMinor() != 1100 {
		t.Fatalf("lines %d total %d", len(s.Lines()), s.TotalMinor())
	}
}

func TestOpenAcceptsAZeroTotalQuote(t *testing.T) {
	in := validInput()
	in.Quote.Lines = []cs.QuoteLine{{CatalogItemID: "ITEM-A"}, {CatalogItemID: "ITEM-B"}}
	in.Quote.Charges = []cs.QuoteCharge{{GroupNo: 1}, {GroupNo: 2}}
	in.Quote.TaxMinor, in.Quote.TotalMinor = 0, 0
	s, err := cs.Open(in, t0)
	wantNoErr(t, err)
	r, err := cs.Restore(s.Snapshot())
	wantNoErr(t, err)
	if r.TotalMinor() != 0 || !reflect.DeepEqual(r.Snapshot(), s.Snapshot()) {
		t.Fatal("a free session round-trips")
	}
}

func TestOpenAcceptsTheLowestCartVersionAndEdgeCurrencies(t *testing.T) {
	in := validInput()
	in.Cart.Version = 1
	in.Quote.Currency = "AZZ"
	s, err := cs.Open(in, t0)
	wantNoErr(t, err)
	r, err := cs.Restore(s.Snapshot())
	wantNoErr(t, err)
	if r.CartVersion() != 1 || r.Currency() != "AZZ" {
		t.Fatalf("snapshot %+v", r.Snapshot())
	}
}

func TestOpenRejectsNegativeAmountsEvenWhenTheTotalAddsUp(t *testing.T) {
	cases := map[string]func(*cs.OpenInput){
		// each keeps total = items + delivery + tax, so only the sign check can refuse it
		"negative charge": func(in *cs.OpenInput) {
			in.Quote.Charges[0].ChargeMinor = -100
			in.Quote.TotalMinor = 1000 + 500 - 100 + 200 + 100
		},
		"negative line": func(in *cs.OpenInput) {
			in.Quote.Lines[0].LineTotalMinor = -1000
			in.Quote.TotalMinor = -1000 + 500 + 300 + 200 + 100
		},
	}
	for name, mut := range cases {
		in := validInput()
		mut(&in)
		if _, err := cs.Open(in, t0); !isErr(err, cs.ErrContractViolation) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
