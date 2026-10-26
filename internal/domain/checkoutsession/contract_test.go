package checkoutsession_test

import (
	"errors"
	"testing"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

func isErr(got, want error) bool { return errors.Is(got, want) }

func TestOpenContractViolations(t *testing.T) {
	cases := map[string]func(*cs.OpenInput){
		"no lines":        func(in *cs.OpenInput) { in.Cart.Lines = nil },
		"no groups":       func(in *cs.OpenInput) { in.Cart.Groups = nil },
		"group order":     func(in *cs.OpenInput) { in.Cart.Groups[1].GroupNo = 1 },
		"group offer":     func(in *cs.OpenInput) { in.Cart.Groups[0].OfferID = "" },
		"group tier":      func(in *cs.OpenInput) { in.Cart.Groups[0].Tier = "BOGUS" },
		"group no lines":  func(in *cs.OpenInput) { in.Cart.Groups[0].LineNos = nil },
		"unknown line":    func(in *cs.OpenInput) { in.Cart.Groups[0].LineNos = []int{3} },
		"zero line":       func(in *cs.OpenInput) { in.Cart.Groups[0].LineNos = []int{0} },
		"duplicate line":  func(in *cs.OpenInput) { in.Cart.Groups[1].LineNos = []int{1} },
		"uncovered line":  func(in *cs.OpenInput) { in.Cart.Groups = in.Cart.Groups[:1] },
		"missing offer":   func(in *cs.OpenInput) { in.Offers = in.Offers[:1] },
		"bad quote id":    func(in *cs.OpenInput) { in.Quote.QuoteID = "" },
		"bad currency":    func(in *cs.OpenInput) { in.Quote.Currency = "usd" },
		"short currency":  func(in *cs.OpenInput) { in.Quote.Currency = "US" },
		"negative tax":    func(in *cs.OpenInput) { in.Quote.TaxMinor = -1 },
		"line count":      func(in *cs.OpenInput) { in.Quote.Lines = in.Quote.Lines[:1] },
		"charge count":    func(in *cs.OpenInput) { in.Quote.Charges = in.Quote.Charges[:1] },
		"unpriced item":   func(in *cs.OpenInput) { in.Quote.Lines[1].CatalogItemID = "ITEM-Z" },
		"repeated item":   func(in *cs.OpenInput) { in.Quote.Lines[1].CatalogItemID = "ITEM-A" },
		"negative line":   func(in *cs.OpenInput) { in.Quote.Lines[0].LineTotalMinor = -1 },
		"negative charge": func(in *cs.OpenInput) { in.Quote.Charges[0].ChargeMinor = -1 },
		"repeat charge":   func(in *cs.OpenInput) { in.Quote.Charges[1].GroupNo = 1 },
		"charge group":    func(in *cs.OpenInput) { in.Quote.Charges[1].GroupNo = 9 },
		"total mismatch":  func(in *cs.OpenInput) { in.Quote.TotalMinor = 2099 },
	}
	for name, mut := range cases {
		in := validInput()
		mut(&in)
		if _, err := cs.Open(in, t0); !isErr(err, cs.ErrContractViolation) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestOpenLineBoundsAreValidation(t *testing.T) {
	in := validInput()
	in.Cart.Lines[0].Quantity = cs.MaxQuantity + 1
	if _, err := cs.Open(in, t0); !isErr(err, shared.ErrValidation) {
		t.Fatalf("quantity: %v", err)
	}
	in = validInput()
	in.Cart.Lines[0].Quantity = cs.MaxQuantity
	if _, err := cs.Open(in, t0); err != nil {
		t.Fatalf("max quantity must pass: %v", err)
	}
	in = validInput()
	in.Cart.Lines[0].CatalogItemID = "bad id"
	if _, err := cs.Open(in, t0); !isErr(err, shared.ErrValidation) {
		t.Fatalf("item id: %v", err)
	}
	in = validInput()
	in.Cart.Lines = make([]cs.CartLine, cs.MaxLines+1)
	if _, err := cs.Open(in, t0); !isErr(err, cs.ErrContractViolation) {
		t.Fatalf("too many lines: %v", err)
	}
}
