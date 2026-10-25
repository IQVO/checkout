package checkoutsession_test

import (
	"testing"
	"time"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

func TestOpenBuildsSessionWithAllocations(t *testing.T) {
	s, err := cs.Open(validInput(), t0)
	wantNoErr(t, err)
	if s.State() != cs.StateOpen || s.Version() != 1 || s.TotalMinor() != 2100 || s.Currency() != "USD" {
		t.Fatalf("unexpected session %+v", s.Snapshot())
	}
	g := s.Groups()
	// group 1: 1000+300 + tax share 100*1300/2000=65; group 2: 500+200 + 35.
	if g[0].AmountMinor != 1365 || g[1].AmountMinor != 735 {
		t.Fatalf("allocations = %d, %d", g[0].AmountMinor, g[1].AmountMinor)
	}
}

func TestOpenNumbersLinesAndRaisesEvent(t *testing.T) {
	s, err := cs.Open(validInput(), t0)
	wantNoErr(t, err)
	l := s.Lines()
	if l[0].LineNo != 1 || l[1].LineNo != 2 || l[1].CatalogItemID != "ITEM-B" {
		t.Fatalf("lines = %+v", l)
	}
	ev := s.PullEvents()
	if len(ev) != 1 || ev[0].EventName() != "CheckoutOpened" || ev[0].SubjectID() != "sess-1" {
		t.Fatalf("events = %+v", ev)
	}
	if s.PullEvents() != nil {
		t.Fatal("PullEvents must clear")
	}
	if s.CartVersion() != 7 || s.QuoteExpiresAt() != t0.Add(15*time.Minute) || s.AuthAttempt() != 0 {
		t.Fatalf("unexpected header fields")
	}
}

func TestOpenTaxRemainderGoesToFirstGroup(t *testing.T) {
	in := validInput()
	in.Quote.TaxMinor = 101
	in.Quote.TotalMinor = 2101
	s, err := cs.Open(in, t0)
	wantNoErr(t, err)
	g := s.Groups()
	if g[0].AmountMinor+g[1].AmountMinor != 2101 {
		t.Fatalf("allocations do not sum: %+v", g)
	}
	if g[1].AmountMinor != 700+35 || g[0].AmountMinor != 1300+66 {
		t.Fatalf("remainder placement wrong: %+v", g)
	}
}

func TestOpenZeroBaseSpreadsNoTax(t *testing.T) {
	in := validInput()
	in.Quote.Lines = []cs.QuoteLine{{CatalogItemID: "ITEM-A"}, {CatalogItemID: "ITEM-B"}}
	in.Quote.Charges = []cs.QuoteCharge{{GroupNo: 1}, {GroupNo: 2}}
	in.Quote.TaxMinor = 5
	in.Quote.TotalMinor = 5
	s, err := cs.Open(in, t0)
	wantNoErr(t, err)
	if s.Groups()[0].AmountMinor != 5 || s.Groups()[1].AmountMinor != 0 {
		t.Fatalf("groups = %+v", s.Groups())
	}
}

func TestOpenCartNotReady(t *testing.T) {
	in := validInput()
	in.Cart.Ready = false
	_, err := cs.Open(in, t0)
	wantErr(t, err, cs.ErrCartNotCheckoutReady)
}

func TestOpenOfferNotQuotable(t *testing.T) {
	cases := map[string]func(*cs.OpenInput){
		"not quoted":    func(in *cs.OpenInput) { in.Offers[1].Quoted = false },
		"inside margin": func(in *cs.OpenInput) { in.Offers[0].ExpiresAt = t0.Add(cs.OfferMargin) },
		"expired":       func(in *cs.OpenInput) { in.Offers[0].ExpiresAt = t0.Add(-time.Hour) },
	}
	for name, mut := range cases {
		in := validInput()
		mut(&in)
		_, err := cs.Open(in, t0)
		if err == nil || !isErr(err, cs.ErrOfferNotQuotable) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	in := validInput()
	in.Offers[0].ExpiresAt = t0.Add(cs.OfferMargin + time.Second)
	if _, err := cs.Open(in, t0); err != nil {
		t.Fatalf("just outside the margin must pass: %v", err)
	}
}

func TestOpenValidation(t *testing.T) {
	cases := map[string]func(*cs.OpenInput){
		"session id":  func(in *cs.OpenInput) { in.ID = "" },
		"cart id":     func(in *cs.OpenInput) { in.Cart.CartID = "bad id" },
		"shopper":     func(in *cs.OpenInput) { in.ShopperID = "" },
		"address":     func(in *cs.OpenInput) { in.ShipToAddressID = "" },
		"cart ver":    func(in *cs.OpenInput) { in.Cart.Version = 0 },
		"quote stale": func(in *cs.OpenInput) { in.Quote.ExpiresAt = t0 },
	}
	for name, mut := range cases {
		in := validInput()
		mut(&in)
		if _, err := cs.Open(in, t0); !isErr(err, shared.ErrValidation) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if _, err := cs.Open(validInput(), time.Time{}); !isErr(err, shared.ErrValidation) {
		t.Fatalf("zero now: %v", err)
	}
}
