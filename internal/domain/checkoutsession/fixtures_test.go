package checkoutsession_test

import (
	"errors"
	"testing"
	"time"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// validInput is a two-line, two-group cart: group 1 ships line 1 (2 x ITEM-A),
// group 2 ships line 2 (1 x ITEM-B). Items 1000 + 500, delivery 300 + 200,
// tax 100, total 2100.
func validInput() cs.OpenInput {
	return cs.OpenInput{
		ID: "sess-1", ShopperID: "shopper-1", ShipToAddressID: "addr-1",
		Cart: cs.CartSnapshot{
			CartID: "cart-1", Version: 7, Ready: true,
			Lines: []cs.CartLine{{CatalogItemID: "ITEM-A", Quantity: 2}, {CatalogItemID: "ITEM-B", Quantity: 1}},
			Groups: []cs.CartGroup{
				{GroupNo: 1, OfferID: "offer-1", Tier: cs.TierNextDay, LineNos: []int{1}},
				{GroupNo: 2, OfferID: "offer-2", Tier: cs.TierStandard, LineNos: []int{2}},
			},
		},
		Offers: []cs.OfferStatus{
			{OfferID: "offer-1", Quoted: true, ExpiresAt: t0.Add(time.Hour)},
			{OfferID: "offer-2", Quoted: true, ExpiresAt: t0.Add(time.Hour)},
		},
		Quote: cs.PriceQuote{
			QuoteID: "quote-1", Currency: "USD",
			Lines:      []cs.QuoteLine{{CatalogItemID: "ITEM-A", LineTotalMinor: 1000}, {CatalogItemID: "ITEM-B", LineTotalMinor: 500}},
			Charges:    []cs.QuoteCharge{{GroupNo: 1, ChargeMinor: 300}, {GroupNo: 2, ChargeMinor: 200}},
			TaxMinor:   100,
			TotalMinor: 2100,
			ExpiresAt:  t0.Add(15 * time.Minute),
		},
	}
}

func opened(t *testing.T) *cs.CheckoutSession {
	t.Helper()
	s, err := cs.Open(validInput(), t0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.PullEvents()
	return s
}

func authorizing(t *testing.T) *cs.CheckoutSession {
	t.Helper()
	s := opened(t)
	if err := s.Confirm("tok_1", t0.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	return s
}

func declined(t *testing.T) *cs.CheckoutSession {
	t.Helper()
	s := authorizing(t)
	if err := s.RecordDecline("insufficient-funds", t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordDecline: %v", err)
	}
	s.PullEvents()
	return s
}

func placing(t *testing.T) *cs.CheckoutSession {
	t.Helper()
	s := authorizing(t)
	if err := s.RecordAuthorization("auth-1", 2100, "USD", []byte(`{"k":1}`), t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordAuthorization: %v", err)
	}
	s.PullEvents()
	return s
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func wantNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
