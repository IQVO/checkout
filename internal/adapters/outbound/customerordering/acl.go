// Package customerordering is the Anti-Corruption Layer to the retail
// network's customer-ordering context, the ONLY place in this service that
// knows the network's wire names. The network calls the step "Checkout"
// (POST /checkout, operation placeOrder); here it is the saga's Place-Order
// Step. The request references the shopper and a saved address
// (shipToAddressId, contracts-delta NG1) and never carries an inline address,
// so the stored body is PII-free and replays byte for byte.
package customerordering

import (
	"encoding/json"
	"fmt"

	"github.com/IQVO/checkout/internal/application/exchange"
)

// Wire shapes of the network's CheckoutRequest. Field order is fixed by the
// struct, so BuildRequest is deterministic.

type wireLine struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type wireGroup struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
	LineNos []int  `json:"lineNos"`
}

type wireCheckout struct {
	ShopperID       string      `json:"shopperId"`
	ShipToAddressID string      `json:"shipToAddressId"`
	Lines           []wireLine  `json:"lines"`
	Groups          []wireGroup `json:"groups"`
}

// BuildRequest renders the place-order request body of a session.
func BuildRequest(d exchange.PlacementDraft) ([]byte, error) {
	w := wireCheckout{ShopperID: d.ShopperID, ShipToAddressID: d.ShipToAddressID}
	for _, l := range d.Lines {
		w.Lines = append(w.Lines, wireLine(l))
	}
	for _, g := range d.Groups {
		w.Groups = append(w.Groups, wireGroup{GroupNo: g.GroupNo, OfferID: g.OfferID, LineNos: g.LineNos})
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("encode place-order request: %w", err)
	}
	return b, nil
}
