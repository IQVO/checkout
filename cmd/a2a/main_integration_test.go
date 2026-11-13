//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

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
// already migrated, a second run must be a no-op), then answers over the real
// JSON-RPC router exactly what the domain saga persisted. A client discovers
// it the way a delegating agent does (ADR 0007): it fetches the Agent Card
// from the well-known path and dials the advertised interface. Rejections are
// answered in-band as problems, and the reads add no outbox row: the agent is
// read-only, so there is nothing for a relay to publish from this binary.
func TestA2AAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
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

	srv := httptest.NewServer(newAgent(deps, "http://placeholder.invalid/", "it", quietLogger(), "checkout-a2a"))
	t.Cleanup(srv.Close)
	client := dialFromServedCard(t, srv)

	assertSeededReads(t, client)
	assertProblemsAreAnsweredInBand(t, client)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the A2A binary wrote an event", before, after)
	}
}

// dialFromServedCard runs the documented discovery flow: fetch the Agent Card
// the binary serves at the well-known path, then point a real a2aclient at
// this server (the card advertises the deployment's URL, which a gateway
// route rewrites the same way for a real caller).
func dialFromServedCard(t *testing.T, srv *httptest.Server) *a2aclient.Client {
	t.Helper()
	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v, want the well-known Agent Card", err, res)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	if len(card.Skills) != 1 || card.Skills[0].ID != "checkout_session_status" {
		t.Fatalf("card skills = %+v, want the one documented skill", card.Skills)
	}
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// skillCall sends one skill request and returns the decoded envelope and the
// raw reply, so every assertion can name what failed.
func skillCall(t *testing.T, client *a2aclient.Client, skill, sessionID string) (map[string]any, string) {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{
		"skill": skill, "arguments": map[string]any{"sessionId": sessionID},
	}))
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("%s: reply = %T, want a message with the data and text parts", skill, resp)
	}
	data, ok := reply.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("%s: first part is %T, want the structured envelope", skill, reply.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env, string(raw)
}

// assertSeededReads reads back, through the documented skill, exactly what
// seedCompletedSession persisted: the session fields of the envelope's data
// part and the one-line summary in the text part.
func assertSeededReads(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	env, raw := skillCall(t, client, "checkout_session_status", "sess-it-1")
	if env["skill"] != "checkout_session_status" || env["problem"] != nil {
		t.Fatalf("envelope = %s, want the skill answer with no problem", raw)
	}
	data, _ := env["data"].(map[string]any)
	session := data["session"].(map[string]any)
	for field, want := range map[string]any{
		"sessionId": "sess-it-1", "state": "COMPLETED", "cartId": "cart-1",
		"shopperId": "shopper-1", "shipToAddressId": "addr-1", "priceQuoteId": "quote-1",
		"currency": "USD", "quoteExpiresAt": "2026-11-02T10:15:00Z",
		"authorizationId": "auth-it-1", "orderId": "ord-it-1",
		"cartVersion": 5.0, "totalMinor": 2100.0, "version": 4.0,
	} {
		if session[field] != want {
			t.Errorf("session.%s = %v, want %v", field, session[field], want)
		}
	}
	if groups := session["groups"].([]any); len(groups) != 2 || groups[0].(map[string]any)["amountMinor"] != 1365.0 {
		t.Errorf("groups = %v, want the two allocated shipment groups", session["groups"])
	}
	if order, _ := session["orderId"].(string); order != "ord-it-1" {
		t.Errorf("orderId = %v, want the placed order of the COMPLETED session", session["orderId"])
	}
}

// assertProblemsAreAnsweredInBand pins ADR 0007's rejection contract: domain
// rejections and bad requests are RFC 7807-shaped problems in the message
// envelope (never JSON-RPC errors), with the REST adapter's classification.
func assertProblemsAreAnsweredInBand(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	for name, tc := range map[string]struct {
		skill, sessionID, problemType string
		status                        int64
	}{
		"unknown session":  {"checkout_session_status", "sess-nope", "/problems/not-found", 404},
		"over-long id":     {"checkout_session_status", longSessionID(), "/problems/not-found", 404},
		"blank session id": {"checkout_session_status", " ", "/problems/invalid-argument", 400},
		"unknown skill":    {"checkout_session_open", "sess-it-1", "/problems/unknown-skill", 400},
	} {
		env, raw := skillCall(t, client, tc.skill, tc.sessionID)
		problem, _ := env["problem"].(map[string]any)
		if problem == nil || problem["type"] != tc.problemType || problem["status"] != float64(tc.status) {
			t.Errorf("%s: problem = %s, want %s (%d) answered in-band", name, raw, tc.problemType, tc.status)
		}
	}
}

// longSessionID is one character over the documented 128-character bound: it
// can never name a session and is a miss on all three surfaces.
func longSessionID() string { return strings.Repeat("x", 129) }

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
