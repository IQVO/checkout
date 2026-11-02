//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/testing/kafkatest"
	"github.com/IQVO/checkout/internal/testing/pgtest"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka with nothing but the environment a deployment sets, and drives it only
// through REST and Kafka. The five sibling services are ONE local fake HTTP
// server answering the routes the saga calls. Every Kafka read is bounded by a
// deadline; run it with -timeout 300s.

const (
	topicEvents    = "retailnetwork.checkout.events"
	topicAnalytics = "retailnetwork.checkout.analytics"
	typePrefix     = "com.retailnetwork.storefront.checkout.checkoutsession."
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	kafkatest.Shutdown()
	os.Exit(code)
}

const cartAnswer = `{"cartId":"cart-1","state":"OPEN","destinationPostalPrefix":"941",
"lines":[{"catalogItemId":"sku-1","quantity":2},{"catalogItemId":"sku-2","quantity":1}],
"deliveryQuote":{"groups":[
 {"groupNo":1,"lineNos":[1],"offers":[{"offerId":"of-1","tier":"NEXT_DAY"}]},
 {"groupNo":2,"lineNos":[2],"offers":[{"offerId":"of-2","tier":"STANDARD"}]}]},
"selections":[{"groupNo":1,"offerId":"of-1"},{"groupNo":2,"offerId":"of-2"}],
"checkoutReadiness":{"ready":true,"allGroupsSelected":true,"noOfferExpired":true},"version":5}`

const offerAnswer = `{"offerId":"x","state":"QUOTED","expiresAt":"2099-01-01T00:00:00Z"}`

// siblings fakes the five services. placeStatus selects what POST /checkout
// answers; every call is recorded.
type siblings struct {
	*httptest.Server
	mu          sync.Mutex
	placeStatus int
	cartDown    bool
	calls       []string
	checkoutKey []string
}

func (s *siblings) setPlaceStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placeStatus = code
}

// setCartDown makes GET /carts/cart-1 answer 503 (shopping-cart unavailable).
func (s *siblings) setCartDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cartDown = down
}

func (s *siblings) called(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func fakeSiblings(t *testing.T) *siblings {
	t.Helper()
	s := &siblings{placeStatus: http.StatusCreated}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *siblings) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	status, down := s.placeStatus, s.cartDown
	if r.URL.Path == "/checkout" {
		s.checkoutKey = append(s.checkoutKey, r.Header.Get("Idempotency-Key"))
	}
	s.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case down && r.Method == http.MethodGet && r.URL.Path == "/carts/cart-1":
		w.WriteHeader(http.StatusServiceUnavailable)
	case r.Method == http.MethodGet && r.URL.Path == "/carts/cart-1":
		_, _ = w.Write([]byte(cartAnswer))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/delivery-offers/"):
		_, _ = w.Write([]byte(offerAnswer))
	case r.URL.Path == "/price-quotes":
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(quoteAnswer()))
	case r.URL.Path == "/payment-authorizations":
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(authorizeAnswer(body))
	case strings.HasSuffix(r.URL.Path, "/voids"):
		_, _ = w.Write([]byte(`{}`))
	case r.URL.Path == "/checkout":
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"orderId":"order-e2e","status":"PLACED"}`))
	default:
		http.NotFound(w, r)
	}
}

func quoteAnswer() string {
	return fmt.Sprintf(`{"quoteId":"q-1","cartId":"cart-1","state":"ISSUED","currency":"USD",
"lines":[{"catalogItemId":"sku-1","lineTotal":{"amountMinor":1000,"currency":"USD"}},{"catalogItemId":"sku-2","lineTotal":{"amountMinor":500,"currency":"USD"}}],
"groups":[{"groupNo":1,"deliveryCharge":{"amountMinor":300,"currency":"USD"}},{"groupNo":2,"deliveryCharge":{"amountMinor":200,"currency":"USD"}}],
"taxMinor":100,"totalMinor":2100,"expiresAt":%q}`, time.Now().Add(15*time.Minute).UTC().Format(time.RFC3339))
}

func authorizeAnswer(body []byte) []byte {
	var req struct {
		CheckoutSessionID string `json:"checkoutSessionId"`
		TotalMinor        int64  `json:"totalMinor"`
	}
	_ = json.Unmarshal(body, &req)
	out, _ := json.Marshal(map[string]any{
		"authorizationId": "auth-" + req.CheckoutSessionID, "checkoutSessionId": req.CheckoutSessionID,
		"state": "AUTHORIZED", "currency": "USD", "totalMinor": req.TotalMinor,
	})
	return out
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}
