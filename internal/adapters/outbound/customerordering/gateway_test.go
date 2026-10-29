package customerordering_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/customerordering"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

type seen struct{ method, path, key, body string }

func serve(t *testing.T, status int, body string) (*customerordering.Gateway, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.method, got.path, got.key, got.body = r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"), string(raw)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return customerordering.New(srv.URL, nil), got
}

var draft = exchange.PlacementDraft{
	ShopperID: "shopper-1", ShipToAddressID: "addr-1",
	Lines:  []cs.Line{{LineNo: 1, CatalogItemID: "A", Quantity: 2}, {LineNo: 2, CatalogItemID: "B", Quantity: 1}},
	Groups: []cs.Group{{GroupNo: 1, OfferID: "o1", Tier: cs.TierNextDay, LineNos: []int{1, 2}, AmountMinor: 99}},
}

const wantBody = `{"shopperId":"shopper-1","shipToAddressId":"addr-1","lines":[{"lineNo":1,"catalogItemId":"A","quantity":2},{"lineNo":2,"catalogItemId":"B","quantity":1}],"groups":[{"groupNo":1,"offerId":"o1","lineNos":[1,2]}]}`

func TestBuildRequestIsPIIFreeDeterministicAndUsesTheAddressReference(t *testing.T) {
	g, _ := serve(t, 201, `{}`)
	first, err := g.BuildRequest(draft)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := customerordering.BuildRequest(draft)
	if string(first) != wantBody || string(second) != wantBody {
		t.Fatalf("body %s", first)
	}
	if strings.Contains(string(first), "shipTo\"") || strings.Contains(string(first), "amountMinor") {
		t.Fatal("never an inline address and never the allocation")
	}
}

func TestPlaceSendsTheStoredBodyWithTheSessionAsKey(t *testing.T) {
	g, got := serve(t, 201, `{"orderId":"order-7","shopperId":"shopper-1","status":"PLACED"}`)
	out, err := g.Place(context.Background(), "sess-1", []byte(wantBody))
	if err != nil || out.Kind != exchange.OutcomePlaced || out.OrderID != "order-7" {
		t.Fatalf("out %+v err %v", out, err)
	}
	if got.method != "POST" || got.path != "/checkout" || got.key != "sess-1" || got.body != wantBody {
		t.Fatalf("request %+v", got)
	}
}

func TestPlaceDefinitive409IsARequote(t *testing.T) {
	body := `{"type":"/problems/offer-not-quotable","title":"x","status":409,"requotedOffers":[
	{"groupNo":1,"offerId":"o9","tier":"SAME_DAY","deliverByEarliest":"2026-10-26T08:00:00Z","deliverByLatest":"2026-10-27T20:00:00Z","expiresAt":"2026-10-25T12:00:00Z"}]}`
	for _, slug := range []string{"offer-not-quotable", "promise-capacity-exhausted", "book-closed"} {
		g, _ := serve(t, 409, strings.ReplaceAll(body, "offer-not-quotable", slug))
		out, err := g.Place(context.Background(), "s", []byte("{}"))
		if err != nil || out.Kind != exchange.OutcomeRequote || len(out.Offers) != 1 {
			t.Fatalf("%s: out %+v err %v", slug, out, err)
		}
		o := out.Offers[0]
		if o.OfferID != "o9" || o.GroupNo != 1 || o.Tier != cs.TierSameDay || o.DeliverByEarliest != "2026-10-26" || o.DeliverByLatest != "2026-10-27" ||
			!o.ExpiresAt.Equal(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)) {
			t.Fatalf("offer %+v", o)
		}
	}
}

func TestPlaceRejectionsAreContractBugs(t *testing.T) {
	for _, status := range []int{400, 422} {
		g, _ := serve(t, status, `{"type":"/problems/validation-failed","detail":"unknown id"}`)
		out, err := g.Place(context.Background(), "s", []byte("{}"))
		if err != nil || out.Kind != exchange.OutcomeRejected || !strings.Contains(out.Detail, "validation-failed") {
			t.Fatalf("%d: out %+v err %v", status, out, err)
		}
	}
}

func TestPlaceEverythingElseIsInDoubt(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"503":               {503, `{}`},
		"500":               {500, ``},
		"concurrent update": {409, `{"type":"/problems/concurrent-update"}`},
		"no order id":       {201, `{"status":"PLACED"}`},
		"undecodable":       {200, `nope`},
		"unexpected 404":    {404, `{}`},
		"undecodable 409":   {409, `{"type":"/problems/book-closed","requotedOffers":"x"}`},
	}
	for name, c := range cases {
		g, _ := serve(t, c.status, c.body)
		if _, err := g.Place(context.Background(), "s", []byte("{}")); !errors.Is(err, ports.ErrOrderingUnavailable) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := customerordering.New("http://127.0.0.1:1", nil).Place(context.Background(), "s", []byte("{}")); !errors.Is(err, ports.ErrOrderingUnavailable) {
		t.Fatalf("transport: %v", err)
	}
}
