package pricing_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/pricing"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

const quoteJSON = `{"quoteId":"q-1","cartId":"cart-1","state":"ISSUED","currency":"USD",
"lines":[{"catalogItemId":"A","quantity":2,"unitPrice":{"amountMinor":500,"currency":"USD"},"lineTotal":{"amountMinor":1000,"currency":"USD"}}],
"groups":[{"groupNo":1,"tier":"NEXT_DAY","deliveryCharge":{"amountMinor":300,"currency":"USD"}}],
"taxMinor":130,"totalMinor":1430,"versions":{"price":1},"issuedAt":"2026-10-25T10:00:00Z","expiresAt":"2026-10-25T10:15:00Z"}`

type seen struct {
	method, path, key string
	body              map[string]any
}

func serve(t *testing.T, status int, body string) (*pricing.Gateway, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.method, got.path, got.key = r.Method, r.URL.Path, r.Header.Get("Idempotency-Key")
		_ = json.Unmarshal(raw, &got.body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return pricing.New(srv.URL, nil), got
}

var request = exchange.QuoteRequest{
	CartID: "cart-1", DestinationPostalPrefix: "941",
	Lines:  []cs.Line{{LineNo: 1, CatalogItemID: "A", Quantity: 2}},
	Groups: []cs.Group{{GroupNo: 1, OfferID: "o1", Tier: cs.TierNextDay, LineNos: []int{1}}},
}

func TestIssueQuoteSendsTheFrozenSessionAndMapsTheAnswer(t *testing.T) {
	g, got := serve(t, 201, quoteJSON)
	q, err := g.IssueQuote(context.Background(), "key-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/price-quotes" || got.key != "key-1" || got.body["cartId"] != "cart-1" || got.body["destinationPostalPrefix"] != "941" {
		t.Fatalf("request %+v", got)
	}
	if q.QuoteID != "q-1" {
		t.Fatalf("quote %+v", q)
	}
}

func TestIssueQuoteRequestGroupsAndAnswer(t *testing.T) {
	g, got := serve(t, 201, quoteJSON)
	q, err := g.IssueQuote(context.Background(), "key-1", request)
	if err != nil {
		t.Fatal(err)
	}
	grp := got.body["groups"].([]any)[0].(map[string]any)
	if grp["offerId"] != "o1" || grp["tier"] != "NEXT_DAY" || grp["groupNo"] != float64(1) {
		t.Fatalf("groups %v", got.body["groups"])
	}
	if q.QuoteID != "q-1" || q.Currency != "USD" || q.TotalMinor != 1430 || q.TaxMinor != 130 || q.Lines[0].LineTotalMinor != 1000 ||
		q.Charges[0].ChargeMinor != 300 || q.Charges[0].GroupNo != 1 || q.ExpiresAt.Minute() != 15 {
		t.Fatalf("quote %+v", q)
	}
}

func TestIssueQuoteFailures(t *testing.T) {
	g, _ := serve(t, 422, `{"type":"/problems/item-not-priced","title":"x","detail":"A is not priced"}`)
	if _, err := g.IssueQuote(context.Background(), "k", request); !errors.Is(err, ports.ErrQuoteRejected) {
		t.Fatalf("422: %v", err)
	}
	g, _ = serve(t, 500, ``)
	if _, err := g.IssueQuote(context.Background(), "k", request); !errors.Is(err, ports.ErrPricingUnavailable) {
		t.Fatalf("500: %v", err)
	}
	g, _ = serve(t, 201, `nope`)
	if _, err := g.IssueQuote(context.Background(), "k", request); !errors.Is(err, ports.ErrPricingUnavailable) {
		t.Fatalf("undecodable: %v", err)
	}
	if _, err := pricing.New("http://127.0.0.1:1", nil).IssueQuote(context.Background(), "k", request); !errors.Is(err, ports.ErrPricingUnavailable) {
		t.Fatalf("transport: %v", err)
	}
}
