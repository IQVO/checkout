package customerpayments_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/customerpayments"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
)

type seen struct {
	method, path, key string
	body              map[string]any
}

func serve(t *testing.T, status int, body string) (*customerpayments.Gateway, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.method, got.path, got.key = r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key")
		got.body = nil
		_ = json.Unmarshal(raw, &got.body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return customerpayments.New(srv.URL, nil), got
}

var req = exchange.AuthorizeRequest{
	SessionID: "sess-1", Token: "tok_1", Currency: "USD", TotalMinor: 2100,
	Allocations: []exchange.Allocation{{GroupNo: 1, AmountMinor: 1365}, {GroupNo: 2, AmountMinor: 735}},
}

func TestAuthorizeSendsTheSpecRequestAndMapsAuthorized(t *testing.T) {
	g, got := serve(t, 201, `{"authorizationId":"auth-1","checkoutSessionId":"sess-1","state":"AUTHORIZED","currency":"USD","totalMinor":2100,"allocations":[]}`)
	a, err := g.Authorize(context.Background(), "sess-1:auth:0", req)
	if err != nil || !a.Authorized || a.ID != "auth-1" || a.TotalMinor != 2100 || a.Currency != "USD" {
		t.Fatalf("auth %+v err %v", a, err)
	}
	if got.method != "POST" || got.path != "/payment-authorizations" || got.key != "sess-1:auth:0" {
		t.Fatalf("request %+v", got)
	}
	if got.body["checkoutSessionId"] != "sess-1" || got.body["paymentMethodToken"] != "tok_1" || got.body["totalMinor"] != float64(2100) || got.body["currency"] != "USD" {
		t.Fatalf("body %v", got.body)
	}
	al := got.body["allocations"].([]any)[1].(map[string]any)
	if al["groupNo"] != float64(2) || al["amountMinor"] != float64(735) {
		t.Fatalf("allocations %v", got.body["allocations"])
	}
}

func TestAuthorizeDeclinedAndRejected(t *testing.T) {
	g, _ := serve(t, 201, `{"authorizationId":"auth-2","state":"DECLINED","currency":"USD","totalMinor":2100}`)
	a, err := g.Authorize(context.Background(), "k", req)
	if err != nil || a.Authorized || a.ID != "auth-2" || a.DeclineReason != "payment-declined" {
		t.Fatalf("auth %+v err %v", a, err)
	}
	g, _ = serve(t, 422, `{"type":"/problems/allocation-mismatch","title":"x"}`)
	a, err = g.Authorize(context.Background(), "k", req)
	if err != nil || a.Authorized || a.DeclineReason != "authorization-rejected: allocation-mismatch" {
		t.Fatalf("auth %+v err %v", a, err)
	}
}

func TestAuthorizeOutcomeUnknown(t *testing.T) {
	for name, status := range map[string]int{"409": 409, "503": 503, "500": 500} {
		g, _ := serve(t, status, `{"type":"/problems/duplicate-authorization"}`)
		if _, err := g.Authorize(context.Background(), "k", req); !errors.Is(err, ports.ErrPaymentsUnavailable) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	g, _ := serve(t, 201, `nope`)
	if _, err := g.Authorize(context.Background(), "k", req); !errors.Is(err, ports.ErrPaymentsUnavailable) {
		t.Fatalf("undecodable: %v", err)
	}
	if _, err := customerpayments.New("http://127.0.0.1:1", nil).Authorize(context.Background(), "k", req); !errors.Is(err, ports.ErrPaymentsUnavailable) {
		t.Fatalf("transport: %v", err)
	}
}

func TestVoidIsIdempotentAndEscapesTheId(t *testing.T) {
	g, got := serve(t, 200, `{}`)
	if err := g.Void(context.Background(), "sess-1:void", "auth/1"); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/payment-authorizations/auth%2F1/voids" || got.key != "sess-1:void" {
		t.Fatalf("request %+v", got)
	}
	for name, c := range map[string]struct {
		status int
		body   string
		ok     bool
	}{
		"404":               {404, `{}`, true},
		"already resolved":  {409, `{"type":"/problems/illegal-transition"}`, true},
		"concurrent update": {409, `{"type":"/problems/concurrent-update"}`, false},
		"provider down":     {503, `{}`, false},
		"unexpected 422":    {422, `{}`, false},
	} {
		g, _ := serve(t, c.status, c.body)
		err := g.Void(context.Background(), "k", "a")
		if c.ok != (err == nil) || (err != nil && !errors.Is(err, ports.ErrPaymentsUnavailable)) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := customerpayments.New("http://127.0.0.1:1", nil).Void(context.Background(), "k", "a"); !errors.Is(err, ports.ErrPaymentsUnavailable) {
		t.Fatalf("transport: %v", err)
	}
}
