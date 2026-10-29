package kafka

import (
	"fmt"
	"time"

	"github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// Wire payloads (CloudEvents `data`, camelCase JSON) pinned by
// apis/asyncapi.yaml. Instants are RFC 3339 in UTC.

type openedData struct {
	SessionID  string    `json:"sessionId"`
	CartID     string    `json:"cartId"`
	ShopperID  string    `json:"shopperId"`
	TotalMinor int64     `json:"totalMinor"`
	OpenedAt   time.Time `json:"openedAt"`
}

type declinedData struct {
	SessionID string    `json:"sessionId"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

type inDoubtData struct {
	SessionID    string    `json:"sessionId"`
	PlacingSince time.Time `json:"placingSince"`
	At           time.Time `json:"at"`
}

type completedGroup struct {
	GroupNo     int    `json:"groupNo"`
	Tier        string `json:"tier"`
	AmountMinor int64  `json:"amountMinor"`
}

type completedData struct {
	SessionID       string           `json:"sessionId"`
	OrderID         string           `json:"orderId"`
	ShopperID       string           `json:"shopperId"`
	CartID          string           `json:"cartId"`
	AuthorizationID string           `json:"authorizationId"`
	Currency        string           `json:"currency"`
	TotalMinor      int64            `json:"totalMinor"`
	Groups          []completedGroup `json:"groups"`
	CompletedAt     time.Time        `json:"completedAt"`
}

type requotedOffer struct {
	OfferID           string    `json:"offerId"`
	GroupNo           int       `json:"groupNo"`
	Tier              string    `json:"tier"`
	DeliverByEarliest string    `json:"deliverByEarliest"`
	DeliverByLatest   string    `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type requoteData struct {
	SessionID      string          `json:"sessionId"`
	RequotedOffers []requotedOffer `json:"requotedOffers"`
	At             time.Time       `json:"at"`
}

type failedData struct {
	SessionID string    `json:"sessionId"`
	Code      string    `json:"code"`
	At        time.Time `json:"at"`
}

type expiredData struct {
	SessionID string    `json:"sessionId"`
	At        time.Time `json:"at"`
}

func payloadFor(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case checkoutsession.CheckoutOpened:
		return openedData{SessionID: string(e.SessionID), CartID: e.CartID, ShopperID: e.ShopperID, TotalMinor: e.TotalMinor, OpenedAt: e.At}, nil
	case checkoutsession.CheckoutDeclined:
		return declinedData{SessionID: string(e.SessionID), Reason: e.Reason, At: e.At}, nil
	case checkoutsession.CheckoutPlacementInDoubt:
		return inDoubtData{SessionID: string(e.SessionID), PlacingSince: e.PlacingSince.UTC(), At: e.At}, nil
	case checkoutsession.CheckoutCompleted:
		return completedPayload(e), nil
	case checkoutsession.CheckoutRequoteRequired:
		return requotePayload(e), nil
	case checkoutsession.CheckoutFailed:
		return failedData{SessionID: string(e.SessionID), Code: e.Code, At: e.At}, nil
	case checkoutsession.CheckoutSessionExpired:
		return expiredData{SessionID: string(e.SessionID), At: e.At}, nil
	}
	return nil, fmt.Errorf("kafka encoder: %T is not a published event", ev)
}

func completedPayload(e checkoutsession.CheckoutCompleted) completedData {
	groups := make([]completedGroup, len(e.Groups))
	for i, g := range e.Groups {
		groups[i] = completedGroup{GroupNo: g.GroupNo, Tier: string(g.Tier), AmountMinor: g.AmountMinor}
	}
	return completedData{
		SessionID: string(e.SessionID), OrderID: e.OrderID, ShopperID: e.ShopperID, CartID: e.CartID,
		AuthorizationID: e.AuthorizationID, Currency: e.Currency, TotalMinor: e.TotalMinor, Groups: groups, CompletedAt: e.At,
	}
}

func requotePayload(e checkoutsession.CheckoutRequoteRequired) requoteData {
	offers := make([]requotedOffer, len(e.RequotedOffers))
	for i, o := range e.RequotedOffers {
		offers[i] = requotedOffer{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: string(o.Tier),
			DeliverByEarliest: o.DeliverByEarliest, DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt.UTC()}
	}
	return requoteData{SessionID: string(e.SessionID), RequotedOffers: offers, At: e.At}
}
