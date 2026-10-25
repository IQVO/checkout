// Package views holds the read shapes the use cases return to the inbound
// adapters. They are plain data; the HTTP adapter maps them to the OpenAPI
// CheckoutSession schema.
package views

import (
	"time"

	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// Session is a checkout session as the API shows it. It never carries the
// stored placement request or the pending PSP token.
type Session struct {
	SessionID       string
	State           checkoutsession.State
	CartID          string
	CartVersion     int64
	ShopperID       string
	ShipToAddressID string
	Lines           []checkoutsession.Line
	Groups          []checkoutsession.Group
	PriceQuoteID    string
	Currency        string
	TotalMinor      int64
	QuoteExpiresAt  time.Time
	AuthorizationID string
	OrderID         string
	RequotedOffers  []checkoutsession.RequotedOffer
	FailureCode     string
	Version         int64
}

// Unsettled reports a session whose saga has not reached a definitive answer
// for the shopper: AUTHORIZING or PLACING. The confirm route answers 202.
func (s Session) Unsettled() bool {
	return s.State == checkoutsession.StateAuthorizing || s.State == checkoutsession.StatePlacing
}

// Of renders the view of an aggregate.
func Of(s *checkoutsession.CheckoutSession) Session {
	return Session{
		SessionID: string(s.ID()), State: s.State(), CartID: s.CartID(), CartVersion: s.CartVersion(),
		ShopperID: s.ShopperID(), ShipToAddressID: s.ShipToAddressID(), Lines: s.Lines(), Groups: s.Groups(),
		PriceQuoteID: s.PriceQuoteID(), Currency: s.Currency(), TotalMinor: s.TotalMinor(),
		QuoteExpiresAt: s.QuoteExpiresAt(), AuthorizationID: s.AuthorizationID(), OrderID: s.OrderID(),
		RequotedOffers: s.RequotedOffers(), FailureCode: s.FailureCode(), Version: s.Version(),
	}
}
