//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/checkout/internal/adapters/inbound/mcp"
	"github.com/IQVO/checkout/internal/adapters/outbound/postgres"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then serves the documented
// read-only tool over the real Postgres repository. What the tool returns is
// exactly what the domain saga persisted, an unknown id is a tool-level
// not-found error (docs/docs/mcp/tools.md), and the reads add no outbox row:
// the MCP binary wires no write use case (ADR 0005), so it can never insert
// into the outbox cmd/api owns.
func TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	countOutbox := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return n
	}

	seedCompletedSession(t, url)
	before := countOutbox()

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	assertToolList(t, session)
	assertSeededReads(t, session)
	assertUnknownIDIsAToolError(t, session)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
	}
}

// assertToolList pins the documented contract (docs/docs/mcp/tools.md): one
// read-only tool, get_checkout_session, annotated readOnly, idempotent and
// closed-world. There is no write, list or search tool.
func assertToolList(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 {
		t.Fatalf("tools = %d, want exactly the one documented tool", len(tools.Tools))
	}
	tool := tools.Tools[0]
	if tool.Name != "get_checkout_session" {
		t.Fatalf("tool name = %q, want get_checkout_session", tool.Name)
	}
	openWorld := true
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint ||
		tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint == openWorld {
		t.Fatalf("tool annotations = %+v, want readOnly+idempotent+closedWorld", tool.Annotations)
	}
}

// assertSeededReads reads back, through the documented tool, exactly what
// seedCompletedSession persisted: every always-present field of the output
// shape, the optional authorizationId/orderId of a COMPLETED session and the
// per-group allocation.
func assertSeededReads(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "get_checkout_session", Arguments: map[string]any{"sessionId": "sess-it-1"},
	})
	if err != nil || res.IsError {
		t.Fatalf("get_checkout_session: err=%v res=%+v", err, res)
	}
	got, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want the documented session object", res.StructuredContent)
	}
	for field, want := range map[string]any{
		"sessionId": "sess-it-1", "state": "COMPLETED", "cartId": "cart-1",
		"shopperId": "shopper-1", "shipToAddressId": "addr-1", "priceQuoteId": "quote-1",
		"currency": "USD", "quoteExpiresAt": "2026-11-02T10:15:00Z",
		"authorizationId": "auth-it-1", "orderId": "ord-it-1",
		"cartVersion": 5.0, "totalMinor": 2100.0, "version": 4.0,
	} {
		if got[field] != want {
			t.Errorf("%s = %v, want %v", field, got[field], want)
		}
	}
	lines := got["lines"].([]any)
	if len(lines) != 2 || lines[0].(map[string]any)["catalogItemId"] != "sku-1" || lines[0].(map[string]any)["quantity"] != 2.0 {
		t.Errorf("lines = %v, want the two numbered cart lines", got["lines"])
	}
	groups := got["groups"].([]any)
	group1 := groups[0].(map[string]any)
	if len(groups) != 2 || group1["offerId"] != "of-1" || group1["tier"] != "NEXT_DAY" || group1["amountMinor"] != 1365.0 {
		t.Errorf("groups = %v, want the allocated shipment groups (group 1 = items 1000 + charge 300 + tax 65)", got["groups"])
	}
	if groups[1].(map[string]any)["amountMinor"] != 735.0 {
		t.Errorf("group 2 amountMinor = %v, want 735 (items 500 + charge 200 + tax 35)", groups[1].(map[string]any)["amountMinor"])
	}
}

// assertUnknownIDIsAToolError pins the documented error path: an unknown or
// over-long session id is a tool-level not-found error (isError true, slug
// vocabulary shared with the REST problem types), never a transport failure.
func assertUnknownIDIsAToolError(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	overLong := strings.Repeat("x", 129)
	for name, id := range map[string]string{"unknown": "sess-nope", "over-long": overLong} {
		res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
			Name: "get_checkout_session", Arguments: map[string]any{"sessionId": id},
		})
		if err != nil {
			t.Fatalf("%s id: transport error %v, want a tool-level error", name, err)
		}
		if !res.IsError || len(res.Content) == 0 {
			t.Fatalf("%s id: res.IsError = %v, want an error result", name, res.IsError)
		}
		if text, ok := res.Content[0].(*sdk.TextContent); !ok || !strings.HasPrefix(text.Text, "not-found:") {
			t.Fatalf("%s id: content = %v, want the not-found slug", name, res.Content)
		}
	}
}

// seedCompletedSession drives sess-it-1 through the domain saga the way
// cmd/api's use cases do (Open, Confirm, RecordAuthorization, RecordPlaced)
// and persists it through the Postgres repository the command side owns. The
// quote (1000 + 500 items, 300 + 200 delivery, 100 tax = 2100) allocates
// group 1 = 1365 and group 2 = 735 minor units.
func seedCompletedSession(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	s, err := cs.Open(cs.OpenInput{
		ID: "sess-it-1", ShopperID: "shopper-1", ShipToAddressID: "addr-1",
		Cart: cs.CartSnapshot{CartID: "cart-1", Version: 5, Ready: true,
			Lines: []cs.CartLine{{CatalogItemID: "sku-1", Quantity: 2}, {CatalogItemID: "sku-2", Quantity: 1}},
			Groups: []cs.CartGroup{
				{GroupNo: 1, OfferID: "of-1", Tier: cs.TierNextDay, LineNos: []int{1}},
				{GroupNo: 2, OfferID: "of-2", Tier: cs.TierStandard, LineNos: []int{2}},
			}},
		Offers: []cs.OfferStatus{
			{OfferID: "of-1", Quoted: true, ExpiresAt: at.Add(time.Hour)},
			{OfferID: "of-2", Quoted: true, ExpiresAt: at.Add(time.Hour)},
		},
		Quote: cs.PriceQuote{QuoteID: "quote-1", Currency: "USD",
			Lines:    []cs.QuoteLine{{CatalogItemID: "sku-1", LineTotalMinor: 1000}, {CatalogItemID: "sku-2", LineTotalMinor: 500}},
			Charges:  []cs.QuoteCharge{{GroupNo: 1, ChargeMinor: 300}, {GroupNo: 2, ChargeMinor: 200}},
			TaxMinor: 100, TotalMinor: 2100, ExpiresAt: at.Add(15 * time.Minute)},
	}, at)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Confirm("tok-it-1", at.Add(time.Minute)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := s.RecordAuthorization("auth-it-1", 2100, "USD", []byte(`{"checkoutSessionId":"sess-it-1","totalMinor":2100}`), at.Add(2*time.Minute)); err != nil {
		t.Fatalf("record authorization: %v", err)
	}
	if err := s.RecordPlaced("ord-it-1", at.Add(3*time.Minute)); err != nil {
		t.Fatalf("record placed: %v", err)
	}
	if err := postgres.NewSessionRepository(pool).Save(ctx, s); err != nil {
		t.Fatalf("save session: %v", err)
	}
}
