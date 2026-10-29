package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/checkout/internal/adapters/inbound/mcp"
	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func seeded(t *testing.T) *sdk.ClientSession {
	t.Helper()
	store := memory.NewStore(memory.NewClock(t0))
	s, err := cs.Open(cs.OpenInput{
		ID: "sess-1", ShopperID: "shopper-1", ShipToAddressID: "addr-1",
		Cart: cs.CartSnapshot{CartID: "cart-1", Version: 2, Ready: true,
			Lines:  []cs.CartLine{{CatalogItemID: "A", Quantity: 2}},
			Groups: []cs.CartGroup{{GroupNo: 1, OfferID: "o1", Tier: cs.TierNextDay, LineNos: []int{1}}}},
		Offers: []cs.OfferStatus{{OfferID: "o1", Quoted: true, ExpiresAt: t0.Add(time.Hour)}},
		Quote: cs.PriceQuote{QuoteID: "q1", Currency: "USD", Lines: []cs.QuoteLine{{CatalogItemID: "A", LineTotalMinor: 1000}},
			Charges: []cs.QuoteCharge{{GroupNo: 1, ChargeMinor: 300}}, TaxMinor: 130, TotalMinor: 1430, ExpiresAt: t0.Add(15 * time.Minute)},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clientT, serverT := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(inboundmcp.NewDeps(store.Sessions())).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func call(t *testing.T, s *sdk.ClientSession, id string) *sdk.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_checkout_session", Arguments: map[string]any{"sessionId": id}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestGetCheckoutSessionReturnsTheRESTShape(t *testing.T) {
	res := call(t, seeded(t), "sess-1")
	body := text(res)
	if res.IsError {
		t.Fatalf("error: %s", body)
	}
	for _, want := range []string{`"sessionId":"sess-1"`, `"state":"OPEN"`, `"totalMinor":1430`, `"amountMinor":1430`, `"shipToAddressId":"addr-1"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("%s lacks %s", body, want)
		}
	}
	if strings.Contains(body, "paymentMethodToken") || strings.Contains(body, "pendingToken") {
		t.Fatal("the PSP token never leaves the service")
	}
}

func TestUnknownAndMalformedIdsAreNotFound(t *testing.T) {
	s := seeded(t)
	for _, id := range []string{"ghost", "", strings.Repeat("x", 129)} {
		res := call(t, s, id)
		if !res.IsError || !strings.HasPrefix(text(res), "not-found:") {
			t.Fatalf("%q: %v %s", id, res.IsError, text(res))
		}
	}
}

func TestTheSurfaceIsOneReadOnlyTool(t *testing.T) {
	tools, err := seeded(t).ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("tools %v err %v", tools, err)
	}
	tool := tools.Tools[0]
	if tool.Name != "get_checkout_session" || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("tool %+v", tool)
	}
	for _, verb := range []string{"open", "confirm", "place", "void", "authorize", "create", "update", "delete", "cancel"} {
		if strings.Contains(tool.Name, verb) {
			t.Fatalf("a write verb %q in tool %q", verb, tool.Name)
		}
	}
}
