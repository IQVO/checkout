package deliverypromise_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/deliverypromise"
	"github.com/IQVO/checkout/internal/application/ports"
)

func serve(t *testing.T, status int, body string) (*deliverypromise.Gateway, *string) {
	t.Helper()
	path := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.Method + " " + r.URL.EscapedPath()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return deliverypromise.New(srv.URL, nil), path
}

func TestGetOfferQuotedAndExpired(t *testing.T) {
	g, path := serve(t, 200, `{"offerId":"o1","state":"QUOTED","expiresAt":"2026-10-25T11:00:00Z"}`)
	o, err := g.GetOffer(context.Background(), "o1")
	if err != nil || !o.Quoted || o.OfferID != "o1" || !o.ExpiresAt.Equal(time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("offer %+v err %v", o, err)
	}
	if *path != "GET /delivery-offers/o1" {
		t.Fatalf("path %q", *path)
	}
	for _, state := range []string{"EXPIRED", "CONSUMED"} {
		g, _ = serve(t, 200, `{"offerId":"o1","state":"`+state+`","expiresAt":"2026-10-25T11:00:00Z"}`)
		if o, _ := g.GetOffer(context.Background(), "o1"); o.Quoted {
			t.Fatalf("%s is not quotable", state)
		}
	}
}

func TestGetOfferUnknownIsNotQuotable(t *testing.T) {
	g, _ := serve(t, 404, `{}`)
	o, err := g.GetOffer(context.Background(), "gone")
	if err != nil || o.Quoted || o.OfferID != "gone" {
		t.Fatalf("offer %+v err %v", o, err)
	}
}

func TestGetOfferFailures(t *testing.T) {
	for name, g := range map[string]*deliverypromise.Gateway{
		"5xx":       func() *deliverypromise.Gateway { g, _ := serve(t, 502, ``); return g }(),
		"undecoded": func() *deliverypromise.Gateway { g, _ := serve(t, 200, `nope`); return g }(),
		"transport": deliverypromise.New("http://127.0.0.1:1", nil),
	} {
		if _, err := g.GetOffer(context.Background(), "o"); !errors.Is(err, ports.ErrDeliveryPromiseUnavailable) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
