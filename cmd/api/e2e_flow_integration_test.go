//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/testing/kafkatest"
	"github.com/IQVO/checkout/internal/testing/pgtest"
)

func bootEnv(t *testing.T, dsn string, brokers []string, sib *siblings) map[string]string {
	t.Helper()
	kafkatest.CreateTopic(t, topicEvents)
	kafkatest.CreateTopic(t, topicAnalytics)
	return map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","),
		"SHOPPING_CART_URL": sib.URL, "DELIVERY_PROMISE_URL": sib.URL, "PRICING_URL": sib.URL,
		"CUSTOMER_PAYMENTS_URL": sib.URL, "CUSTOMER_ORDERING_URL": sib.URL,
		"OUTBOX_RELAY_INTERVAL": "100ms", "INDOUBT_SWEEP_INTERVAL": "200ms", "STUCK_SWEEP_INTERVAL": "200ms",
		"VOID_SWEEP_INTERVAL": "200ms", "EXPIRY_SWEEP_INTERVAL": "200ms", "PURGE_SWEEP_INTERVAL": "200ms",
		"SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	}
}

const (
	openBody    = `{"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}`
	confirmBody = `{"paymentMethodToken":"tok_1"}`
)

func TestRealWiringPlacesTheOrderAndPublishesTheEvents(t *testing.T) {
	sib := fakeSiblings(t)
	dsn := pgtest.URL(t)
	svc := boot(t, bootEnv(t, dsn, kafkatest.Brokers(t), sib))

	opened := svc.call(t, "POST", "/checkout-sessions", "open-1", openBody)
	if opened.status != http.StatusCreated {
		t.Fatalf("open = %d %s", opened.status, opened.body)
	}
	id, _ := opened.json(t)["sessionId"].(string)
	done := svc.call(t, "POST", "/checkout-sessions/"+id+"/confirmations", "confirm-1", confirmBody)
	m := done.json(t)
	if done.status != http.StatusOK || m["state"] != "COMPLETED" || m["orderId"] != "order-e2e" {
		t.Fatalf("confirm = %d %s", done.status, done.body)
	}
	if got := svc.call(t, "GET", "/checkout-sessions/"+id, "", ""); got.json(t)["state"] != "COMPLETED" {
		t.Fatalf("get = %s", got.body)
	}
	if sib.checkoutKey[0] != id {
		t.Fatalf("the place-order Idempotency-Key must be the sessionId, got %v", sib.checkoutKey)
	}

	events := kafkatest.ReadN(t, topicEvents, 1, 60*time.Second)
	if !strings.Contains(string(events[0].Value), typePrefix+"CheckoutCompleted") || events[0].Key != id {
		t.Fatalf("events topic: %s", events[0].Value)
	}
	analytics := kafkatest.ReadN(t, topicAnalytics, 1, 60*time.Second)
	if !strings.Contains(string(analytics[0].Value), typePrefix+"CheckoutOpened") {
		t.Fatalf("analytics topic: %s", analytics[0].Value)
	}
}

func TestRealWiringResolvesAnInDoubtPlacement(t *testing.T) {
	sib := fakeSiblings(t)
	sib.setPlaceStatus(http.StatusServiceUnavailable)
	dsn := pgtest.URL(t)
	svc := boot(t, bootEnv(t, dsn, kafkatest.Brokers(t), sib))

	id, _ := svc.call(t, "POST", "/checkout-sessions", "open-2", openBody).json(t)["sessionId"].(string)
	r := svc.call(t, "POST", "/checkout-sessions/"+id+"/confirmations", "confirm-2", confirmBody)
	if r.status != http.StatusAccepted || r.json(t)["state"] != "PLACING" {
		t.Fatalf("confirm = %d %s", r.status, r.body)
	}
	if sib.called("POST /payment-authorizations/") != 0 {
		t.Fatal("PLACING is the semantic lock: no void while the outcome is unknown")
	}
	sib.setPlaceStatus(http.StatusCreated)
	pgtest.ExecOn(t, dsn, `UPDATE checkout_sessions SET next_attempt_at = now() - interval '1 minute' WHERE session_id = $1`, id)
	eventually(t, 60*time.Second, "the resolver to complete the session", func() bool {
		return svc.call(t, "GET", "/checkout-sessions/"+id, "", "").json(t)["state"] == "COMPLETED"
	})
	for _, k := range sib.checkoutKey {
		if k != id {
			t.Fatalf("every replay uses the sessionId as key: %v", sib.checkoutKey)
		}
	}
}

func TestRealWiringReadyzIs503WhileKafkaIsDown(t *testing.T) {
	sib := fakeSiblings(t)
	svc := boot(t, bootEnv(t, pgtest.URL(t), kafkatest.Brokers(t), sib))
	if r := svc.call(t, "GET", "/readyz", "", ""); r.status != http.StatusOK {
		t.Fatalf("readyz = %d %s", r.status, r.body)
	}
	kafkatest.Stop(t)
	eventually(t, 60*time.Second, "readyz to report 503", func() bool {
		return svc.call(t, "GET", "/readyz", "", "").status == http.StatusServiceUnavailable
	})
	r := svc.call(t, "GET", "/readyz", "", "")
	if !strings.Contains(r.body, "dependency-unavailable") || !strings.Contains(r.body, "kafka") {
		t.Fatalf("readyz = %d %s", r.status, r.body)
	}
	if h := svc.call(t, "GET", "/healthz", "", ""); h.status != http.StatusOK {
		t.Fatalf("healthz = %d", h.status)
	}
}
