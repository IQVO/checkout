package kafka_test

import (
	"strings"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/kafka"
	"github.com/IQVO/checkout/internal/application/outbox"
	"github.com/IQVO/checkout/internal/domain/shared"
)

type otherEvent struct{ shared.Header }

func (otherEvent) EventName() string { return "Other" }
func (otherEvent) Entity() string    { return "other" }
func (otherEvent) SubjectID() string { return "x" }

func TestEncodeMintsAnIDPerEventAndKeepsOrder(t *testing.T) {
	msgs, err := kafka.NewEncoder().Encode(opened(), declined(), completed())
	if err != nil || len(msgs) != 3 {
		t.Fatalf("Encode: %v (%d)", err, len(msgs))
	}
	if msgs[0].EventID == msgs[1].EventID || msgs[1].EventID == msgs[2].EventID || msgs[0].EventID == "" {
		t.Fatalf("ids must differ: %+v", msgs)
	}
	if !strings.HasSuffix(msgs[0].EventType, ".CheckoutOpened") || !strings.HasSuffix(msgs[1].EventType, ".CheckoutDeclined") || !strings.HasSuffix(msgs[2].EventType, ".CheckoutCompleted") {
		t.Fatalf("order lost: %+v", msgs)
	}
}

func TestEncodeRefusesAnUnpublishedEvent(t *testing.T) {
	if _, err := kafka.NewEncoder().Encode(otherEvent{}); err == nil {
		t.Fatal("an event the service does not publish is a programming error")
	}
	if _, err := (&kafka.Encoder{}).Encode(otherEvent{}); err == nil {
		t.Fatal("the zero Encoder still refuses it")
	}
	msgs, err := (&kafka.Encoder{}).Encode()
	if err != nil || len(msgs) != 0 {
		t.Fatalf("no events encode to nothing: %v %v", msgs, err)
	}
}

func TestEncodeFailsOnAnEmptySubject(t *testing.T) {
	ev := failed()
	ev.SessionID = ""
	if _, err := kafka.NewEncoder().Encode(ev); err == nil {
		t.Fatal("an event without a subject cannot be keyed")
	}
}

func TestEncodeUsesTheZeroEncoderWithUUIDs(t *testing.T) {
	msgs, err := (&kafka.Encoder{}).Encode(opened())
	if err != nil || len(msgs) != 1 || len(msgs[0].EventID) != 36 {
		t.Fatalf("Encode: %v %+v", err, msgs)
	}
}

func TestEncoderTopicOverrideAppliesToEveryEvent(t *testing.T) {
	msgs, err := (&kafka.Encoder{Topic: "custom.topic"}).Encode(opened(), completed())
	if err != nil || msgs[0].Topic != "custom.topic" || msgs[1].Topic != "custom.topic" {
		t.Fatalf("Encode: %v %+v", err, msgs)
	}
}

func TestHeadersConvertToKafkaHeaders(t *testing.T) {
	got := kafka.Headers([]outbox.Header{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}})
	if len(got) != 2 || got[0].Key != "a" || string(got[0].Value) != "1" || got[1].Key != "b" || string(got[1].Value) != "2" {
		t.Fatalf("headers = %+v", got)
	}
}
