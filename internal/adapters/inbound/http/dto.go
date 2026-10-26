package http

import (
	"time"

	"github.com/IQVO/checkout/internal/application/views"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// Wire shapes of apis/openapi.yaml (camelCase JSON). The domain never sees
// them; these structs are the only place JSON names live.

type openRequest struct {
	CartID          string `json:"cartId"`
	ShopperID       string `json:"shopperId"`
	ShipToAddressID string `json:"shipToAddressId"`
}

type confirmRequest struct {
	PaymentMethodToken string `json:"paymentMethodToken"`
}

type lineOut struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type groupOut struct {
	GroupNo     int    `json:"groupNo"`
	OfferID     string `json:"offerId"`
	Tier        string `json:"tier"`
	LineNos     []int  `json:"lineNos"`
	AmountMinor int64  `json:"amountMinor"`
}

type requotedOut struct {
	OfferID           string    `json:"offerId"`
	GroupNo           int       `json:"groupNo"`
	Tier              string    `json:"tier"`
	DeliverByEarliest string    `json:"deliverByEarliest"`
	DeliverByLatest   string    `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type sessionOut struct {
	SessionID       string        `json:"sessionId"`
	State           string        `json:"state"`
	CartID          string        `json:"cartId"`
	CartVersion     int64         `json:"cartVersion"`
	ShopperID       string        `json:"shopperId"`
	ShipToAddressID string        `json:"shipToAddressId"`
	Lines           []lineOut     `json:"lines"`
	Groups          []groupOut    `json:"groups"`
	PriceQuoteID    string        `json:"priceQuoteId"`
	Currency        string        `json:"currency"`
	TotalMinor      int64         `json:"totalMinor"`
	QuoteExpiresAt  time.Time     `json:"quoteExpiresAt"`
	AuthorizationID *string       `json:"authorizationId"`
	OrderID         *string       `json:"orderId"`
	RequotedOffers  []requotedOut `json:"requotedOffers,omitempty"`
	FailureCode     *string       `json:"failureCode"`
	Version         int64         `json:"version"`
}

func sessionBody(v views.Session) sessionOut {
	out := sessionOut{
		SessionID: v.SessionID, State: string(v.State), CartID: v.CartID, CartVersion: v.CartVersion, ShopperID: v.ShopperID,
		ShipToAddressID: v.ShipToAddressID, PriceQuoteID: v.PriceQuoteID, Currency: v.Currency, TotalMinor: v.TotalMinor,
		QuoteExpiresAt: v.QuoteExpiresAt.UTC(), AuthorizationID: optional(v.AuthorizationID), OrderID: optional(v.OrderID),
		FailureCode: optional(v.FailureCode), Version: v.Version,
		Lines: make([]lineOut, len(v.Lines)), Groups: make([]groupOut, len(v.Groups)),
	}
	for i, l := range v.Lines {
		out.Lines[i] = lineOut(l)
	}
	for i, g := range v.Groups {
		out.Groups[i] = groupOut{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: string(g.Tier), LineNos: g.LineNos, AmountMinor: g.AmountMinor}
	}
	if v.State == checkoutsession.StateRequoteRequired {
		out.RequotedOffers = make([]requotedOut, len(v.RequotedOffers))
		for i, o := range v.RequotedOffers {
			out.RequotedOffers[i] = requotedOut{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: string(o.Tier),
				DeliverByEarliest: o.DeliverByEarliest, DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt.UTC()}
		}
	}
	return out
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
