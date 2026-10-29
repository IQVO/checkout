package kafka_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/checkout/internal/adapters/outbound/kafka"
	"github.com/IQVO/checkout/internal/application/outbox"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var t0 = time.Date(2026, 10, 23, 10, 15, 30, 123456000, time.UTC)

const fixedID = "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40"

func hdr(v int64) shared.Header { return shared.Header{Version: v, At: t0} }

func opened() cs.CheckoutOpened {
	return cs.CheckoutOpened{Header: hdr(1), SessionID: "sess-1", CartID: "cart-1", ShopperID: "shopper-1", TotalMinor: 2100}
}

func declined() cs.CheckoutDeclined {
	return cs.CheckoutDeclined{Header: hdr(3), SessionID: "sess-1", Reason: "insufficient-funds"}
}

func inDoubt() cs.CheckoutPlacementInDoubt {
	return cs.CheckoutPlacementInDoubt{Header: hdr(4), SessionID: "sess-1", PlacingSince: t0.Add(-time.Minute)}
}

func completed() cs.CheckoutCompleted {
	return cs.CheckoutCompleted{
		Header: hdr(5), SessionID: "sess-1", OrderID: "order-1", ShopperID: "shopper-1", CartID: "cart-1",
		AuthorizationID: "auth-1", Currency: "USD", TotalMinor: 2100,
		Groups: []cs.Group{{GroupNo: 1, Tier: cs.TierNextDay, AmountMinor: 1365}, {GroupNo: 2, Tier: cs.TierStandard, AmountMinor: 735}},
	}
}

func requote() cs.CheckoutRequoteRequired {
	return cs.CheckoutRequoteRequired{Header: hdr(5), SessionID: "sess-1", RequotedOffers: []cs.RequotedOffer{{
		OfferID: "offer-9", GroupNo: 1, Tier: cs.TierSameDay, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: t0.Add(time.Hour),
	}}}
}

func failed() cs.CheckoutFailed {
	return cs.CheckoutFailed{Header: hdr(5), SessionID: "sess-1", Code: "place-order-rejected"}
}

func expired() cs.CheckoutSessionExpired {
	return cs.CheckoutSessionExpired{Header: hdr(2), SessionID: "sess-1"}
}

func encodeOne(t *testing.T, ev shared.Event) outbox.Message {
	t.Helper()
	enc := &kafka.Encoder{NewID: func() string { return fixedID }}
	msgs, err := enc.Encode(ev)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Encode: %v (%d messages)", err, len(msgs))
	}
	return msgs[0]
}

// assertGolden compares the CloudEvents bytes with the golden file EXACTLY.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, append(got, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("%s differs from its golden file:\n got %s\nwant %s", name, got, want)
	}
}

// assertEnvelope checks the attributes of one message on the given stream.
func assertEnvelope(t *testing.T, msg outbox.Message, name, stream string) {
	t.Helper()
	wantType := "com.retailnetwork.storefront.checkout.checkoutsession." + name
	wantTopic := "retailnetwork.checkout." + stream
	if msg.EventType != wantType || msg.EventID != fixedID || msg.Topic != wantTopic {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Subject != "sess-1" || string(msg.Key) != "sess-1" {
		t.Fatalf("subject/key = %q %q", msg.Subject, msg.Key)
	}
	if msg.DataSchema != "urn:retailnetwork:checkout:"+stream+":"+name+":v1" {
		t.Fatalf("dataschema = %q", msg.DataSchema)
	}
	if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" || msg.Headers[0].Value != cloudevents.MediaType {
		t.Fatalf("headers = %+v", msg.Headers)
	}
	e, err := cloudevents.Decode(msg.Value)
	if err != nil || e.Type() != wantType || e.Source() != "/retailnetwork/checkout" || e.ID() != fixedID || e.Subject() != "sess-1" {
		t.Fatalf("decode: %v %v", err, e)
	}
}

func TestCheckoutOpenedGolden(t *testing.T) {
	msg := encodeOne(t, opened())
	assertGolden(t, "CheckoutOpened", msg.Value)
	assertEnvelope(t, msg, "CheckoutOpened", "analytics")
}

func TestCheckoutDeclinedGolden(t *testing.T) {
	msg := encodeOne(t, declined())
	assertGolden(t, "CheckoutDeclined", msg.Value)
	assertEnvelope(t, msg, "CheckoutDeclined", "analytics")
}

func TestCheckoutPlacementInDoubtGolden(t *testing.T) {
	msg := encodeOne(t, inDoubt())
	assertGolden(t, "CheckoutPlacementInDoubt", msg.Value)
	assertEnvelope(t, msg, "CheckoutPlacementInDoubt", "analytics")
}

func TestCheckoutCompletedGoldenGoesToTheEventsTopic(t *testing.T) {
	msg := encodeOne(t, completed())
	assertGolden(t, "CheckoutCompleted", msg.Value)
	assertEnvelope(t, msg, "CheckoutCompleted", "events")
}

func TestCheckoutRequoteRequiredGolden(t *testing.T) {
	msg := encodeOne(t, requote())
	assertGolden(t, "CheckoutRequoteRequired", msg.Value)
	assertEnvelope(t, msg, "CheckoutRequoteRequired", "analytics")
}

func TestCheckoutFailedGolden(t *testing.T) {
	msg := encodeOne(t, failed())
	assertGolden(t, "CheckoutFailed", msg.Value)
	assertEnvelope(t, msg, "CheckoutFailed", "analytics")
}

func TestCheckoutSessionExpiredGolden(t *testing.T) {
	msg := encodeOne(t, expired())
	assertGolden(t, "CheckoutSessionExpired", msg.Value)
	assertEnvelope(t, msg, "CheckoutSessionExpired", "analytics")
}

func TestCompletedDataCarriesTheModelFields(t *testing.T) {
	var env struct{ Data map[string]any }
	if err := json.Unmarshal(encodeOne(t, completed()).Value, &env); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sessionId", "orderId", "shopperId", "cartId", "authorizationId", "currency", "totalMinor", "groups", "completedAt"} {
		if _, ok := env.Data[k]; !ok {
			t.Fatalf("data %v lacks %s", env.Data, k)
		}
	}
}
