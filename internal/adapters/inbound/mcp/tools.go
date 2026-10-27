package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/usecases"
	"github.com/IQVO/checkout/internal/application/views"
)

// Deps is everything the MCP tools need, injected by the composition root. It
// carries the SAME read use case the REST adapter uses (GET
// /checkout-sessions/{sessionId}) and no write use case at all.
type Deps struct {
	GetSession *usecases.GetSession
}

// NewDeps wires the read use case over the one read repository it needs.
// Handing the repository over this way keeps cmd/mcp from ever naming the
// shared usecases.Deps bundle, which also carries the write seams (the outbox,
// the sibling gateways) no MCP binary may reach.
func NewDeps(sessions ports.SessionRepository) Deps {
	return Deps{GetSession: &usecases.GetSession{Deps: usecases.Deps{Sessions: sessions}}}
}

// maxSessionIDLength mirrors the REST adapter's {sessionId} guard: an id that
// cannot name a session is a miss, never a server error.
const maxSessionIDLength = 128

type sessionIDInput struct {
	SessionID string `json:"sessionId" jsonschema:"the checkout session id, e.g. sess-3f9c1a2b4d5e"`
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

// getCheckoutSession mirrors GET /checkout-sessions/{sessionId}.
func (d Deps) getCheckoutSession(ctx context.Context, in sessionIDInput) (sessionView, error) {
	if in.SessionID == "" || len(in.SessionID) > maxSessionIDLength {
		return sessionView{}, toolError("not-found", "a session id is 1 to 128 characters; this one cannot name a session")
	}
	view, err := d.GetSession.Execute(ctx, in.SessionID)
	if err != nil {
		return sessionView{}, mapError(ctx, err)
	}
	return toSession(view), nil
}

func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_checkout_session",
		Description: "Read a checkout session by id: its state (OPEN, AUTHORIZING, DECLINED, PLACING, REQUOTE_REQUIRED, COMPLETED, " +
			"EXPIRED or FAILED), the cart and shopper references, the numbered lines and shipment groups (groupNo, offerId, tier, " +
			"lineNos and the amountMinor allocated to the group), the binding total and currency, when the price quote expires, the " +
			"payment authorization id, the placed order id, the re-quoted offers of a REQUOTE_REQUIRED session and the failure code " +
			"of a FAILED one. PLACING means the place-order outcome is unknown. Read-only; an unknown or malformed session id fails " +
			"with not-found.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIDInput) (*mcp.CallToolResult, sessionView, error) {
		out, err := d.getCheckoutSession(ctx, in)
		return nil, out, err
	})
}
