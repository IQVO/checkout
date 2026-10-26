package kafka

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/checkout/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/checkout/internal/application/outbox"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// The two topics this service writes. CheckoutCompleted is the only
// integration event (consumed by shopping-cart and customer-payments); the
// other six are analytics-only.
const (
	EventsTopic    = "retailnetwork.checkout.events"
	AnalyticsTopic = "retailnetwork.checkout.analytics"
)

// schemaVersion is the dataschema version of every payload.
const schemaVersion = 1

// Encoder implements outbox.Encoder: each domain event becomes one outbox
// message holding the CloudEvents bytes, the Kafka key (the subject) and the
// content-type header. The CloudEvents `id` is minted HERE, once, and persisted
// with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
	// Topic overrides the destination of EVERY event (the events and analytics
	// topics when empty); tests use it to isolate their topic on a shared broker.
	Topic string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ outbox.Encoder = (*Encoder)(nil)

// Encode encodes events in order. An event type it does not publish is a
// programming error and fails the whole call.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		stream, topic := route(ev)
		if e.Topic != "" {
			topic = e.Topic
		}
		msg, err := buildMessage(ev, newID(), topic, stream, data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// route returns the CloudEvents stream and the topic of an event.
func route(ev shared.Event) (stream, topic string) {
	if _, ok := ev.(checkoutsession.CheckoutCompleted); ok {
		return cloudevents.StreamEvents, EventsTopic
	}
	return cloudevents.StreamAnalytics, AnalyticsTopic
}

func buildMessage(ev shared.Event, id, topic, stream string, data any) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(), Time: ev.OccurredAt(),
		Stream: stream, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID: id, Topic: topic, EventType: cloudevents.Type(ev.Entity(), ev.EventName()), Subject: ev.SubjectID(),
		Key: []byte(ev.SubjectID()), DataSchema: cloudevents.DataSchema(stream, ev.EventName(), schemaVersion),
		Value: value, Headers: []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

// Headers converts the stored outbox headers to Kafka headers.
func Headers(hs []outbox.Header) []kafkago.Header {
	out := make([]kafkago.Header, len(hs))
	for i, h := range hs {
		out[i] = kafkago.Header{Key: h.Key, Value: []byte(h.Value)}
	}
	return out
}

// Writer is the part of *kafkago.Writer the relay sink needs.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// Sink writes outbox messages to Kafka; each message names its own topic, so
// the writer behind it has none (see NewWriter).
type Sink struct{ W Writer }

// Send writes msgs, in order, and returns when the broker acknowledged them.
func (s Sink) Send(ctx context.Context, msgs ...outbox.Message) error {
	out := make([]kafkago.Message, len(msgs))
	for i, m := range msgs {
		out[i] = kafkago.Message{Topic: m.Topic, Key: m.Key, Value: m.Value, Headers: Headers(m.Headers)}
	}
	if err := s.W.WriteMessages(ctx, out...); err != nil {
		return fmt.Errorf("write %d messages to kafka: %w", len(out), err)
	}
	return nil
}
