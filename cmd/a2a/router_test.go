package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	inbounda2a "github.com/IQVO/checkout/internal/adapters/inbound/a2a"
	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// newTestAgent builds the handler exactly as run() does (newAgent), over an
// in-memory repository holding one OPEN session. The memory adapter is a test
// double: it is imported by this _test file only, never by the binary.
func newTestAgent(t *testing.T) http.Handler {
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
	return newAgent(inbounda2a.NewDeps(store.Sessions()), testAgentURL, "test", quietLogger(), "checkout-a2a")
}

func TestRouter_HealthzIsOpenAndServesOK(t *testing.T) {
	h := newTestAgent(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %q, want 200 {\"status\":\"ok\"}", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /healthz Content-Type = %q, want application/json", ct)
	}
}

func TestRouter_ServesTheAgentCardAtTheWellKnownPath(t *testing.T) {
	h := newTestAgent(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent card: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"checkout"`, testAgentURL, `"checkout_session_status"`} {
		if !strings.Contains(body, want) {
			t.Errorf("agent card lacks %s: %s", want, body)
		}
	}
}

// TestRouter_BothMountsReachTheJSONRPCHandler pins the fleet's
// <CTX>_A2A_ENDPOINT convention: the endpoint answers at "/" and at "/a2a",
// and the use case behind it is the wired one (the seeded session is read),
// with no credentials of any kind.
func TestRouter_BothMountsReachTheJSONRPCHandler(t *testing.T) {
	h := newTestAgent(t)
	for _, path := range []string{"/", "/a2a"} {
		for _, auth := range []string{"", "Bearer nonsense"} {
			t.Run(path+" auth="+auth, func(t *testing.T) {
				body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"checkout_session_status","arguments":{"sessionId":"sess-1"}}}]}}}`
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("A2A-Version", "1.0")
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				raw, _ := io.ReadAll(rec.Body)
				if rec.Code != http.StatusOK || !strings.Contains(string(raw), `"jsonrpc"`) {
					t.Fatalf("POST %s: %d %q does not look like a JSON-RPC reply", path, rec.Code, raw)
				}
				if !strings.Contains(string(raw), `\"totalMinor\":1430`) && !strings.Contains(string(raw), `"totalMinor":1430`) {
					t.Fatalf("POST %s: reply %q lacks the seeded session read through the wired use case", path, raw)
				}
			})
		}
	}
}

// TestRouter_ACardFetchedFromTheBinaryIsDialable runs the discovery flow a
// delegating agent runs against the real router: fetch the card, build a
// client from it, call the skill.
func TestRouter_ACardFetchedFromTheBinaryIsDialable(t *testing.T) {
	srv := httptest.NewServer(newTestAgent(t))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	// The card advertises the configured URL, not the test server's: point the
	// client at the test server the way a gateway-rewritten route would.
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": "checkout_session_status", "arguments": map[string]any{"sessionId": "sess-1"}}))})
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T", resp)
	}
	if text, _ := msg.Parts[1].Content.(a2a.Text); !strings.Contains(string(text), "sess-1: OPEN") {
		t.Fatalf("summary = %q, want the seeded session", text)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("CO_A2A_TEST_SET", "v")
	t.Setenv("CO_A2A_TEST_EMPTY", "")
	if got := getenv("CO_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("CO_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
