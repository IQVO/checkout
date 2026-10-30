package a2a

import (
	"fmt"
	"time"

	"github.com/IQVO/checkout/internal/application/views"
)

// problemTypeBase is the namespace of this surface's problem types. It is the
// SAME one the REST adapter uses for its RFC 9457 `type` URIs
// (internal/adapters/inbound/http problemBase), so a delegating agent sees the
// identifiers a REST caller would for the outcomes both surfaces share.
const problemTypeBase = "/problems/"

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: problemTypeBase + "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: problemTypeBase + "unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: 400}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: problemTypeBase + "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: 400}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field, keeping the envelope
// flat and versioned as one contract.
type skillDTO struct {
	Session *sessionView `json:"session,omitempty"`
	summary string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// sessionView mirrors the MCP tool's DTO of the same read (and the REST wire):
// references only (shopperId, shipToAddressId), never the PSP token. Domain
// types never cross the boundary.
type sessionView struct {
	SessionID       string         `json:"sessionId"`
	State           string         `json:"state"`
	CartID          string         `json:"cartId"`
	CartVersion     int64          `json:"cartVersion"`
	ShopperID       string         `json:"shopperId"`
	ShipToAddressID string         `json:"shipToAddressId"`
	Lines           []lineView     `json:"lines"`
	Groups          []groupView    `json:"groups"`
	PriceQuoteID    string         `json:"priceQuoteId"`
	Currency        string         `json:"currency"`
	TotalMinor      int64          `json:"totalMinor"`
	QuoteExpiresAt  string         `json:"quoteExpiresAt"`
	AuthorizationID string         `json:"authorizationId,omitempty"`
	OrderID         string         `json:"orderId,omitempty"`
	RequotedOffers  []requotedView `json:"requotedOffers,omitempty"`
	FailureCode     string         `json:"failureCode,omitempty"`
	Version         int64          `json:"version"`
}

type lineView struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type groupView struct {
	GroupNo     int    `json:"groupNo"`
	OfferID     string `json:"offerId"`
	Tier        string `json:"tier"`
	LineNos     []int  `json:"lineNos"`
	AmountMinor int64  `json:"amountMinor"`
}

type requotedView struct {
	OfferID           string `json:"offerId"`
	GroupNo           int    `json:"groupNo"`
	Tier              string `json:"tier"`
	DeliverByEarliest string `json:"deliverByEarliest"`
	DeliverByLatest   string `json:"deliverByLatest"`
	ExpiresAt         string `json:"expiresAt"`
}

func toSession(v views.Session) sessionView {
	out := sessionView{
		SessionID: v.SessionID, State: string(v.State), CartID: v.CartID, CartVersion: v.CartVersion, ShopperID: v.ShopperID,
		ShipToAddressID: v.ShipToAddressID, PriceQuoteID: v.PriceQuoteID, Currency: v.Currency, TotalMinor: v.TotalMinor,
		QuoteExpiresAt: v.QuoteExpiresAt.UTC().Format(time.RFC3339), AuthorizationID: v.AuthorizationID, OrderID: v.OrderID,
		FailureCode: v.FailureCode, Version: v.Version, Lines: make([]lineView, len(v.Lines)), Groups: make([]groupView, len(v.Groups)),
	}
	for i, l := range v.Lines {
		out.Lines[i] = lineView(l)
	}
	for i, g := range v.Groups {
		out.Groups[i] = groupView{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: string(g.Tier), LineNos: g.LineNos, AmountMinor: g.AmountMinor}
	}
	for _, o := range v.RequotedOffers {
		out.RequotedOffers = append(out.RequotedOffers, requotedView{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: string(o.Tier),
			DeliverByEarliest: o.DeliverByEarliest, DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt.UTC().Format(time.RFC3339)})
	}
	return out
}

// summarize is the one-line text form of a session.
func summarize(s sessionView) string {
	out := fmt.Sprintf("%s: %s, %d line(s) in %d group(s), total %d %s (minor units), quote expires %s",
		s.SessionID, s.State, len(s.Lines), len(s.Groups), s.TotalMinor, s.Currency, s.QuoteExpiresAt)
	if s.OrderID != "" {
		out += ", order " + s.OrderID
	}
	if s.State == "PLACING" {
		out += "; the place-order outcome is unknown, ask again later"
	}
	return out
}
