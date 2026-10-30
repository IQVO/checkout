package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// seededSessions holds one OPEN session, sess-1, opened through the real
// aggregate (the same fixture the MCP tool tests read).
func seededSessions(t *testing.T) ports.SessionRepository {
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
	return store.Sessions()
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireSession struct {
	SessionID       string `json:"sessionId"`
	State           string `json:"state"`
	CartID          string `json:"cartId"`
	ShopperID       string `json:"shopperId"`
	ShipToAddressID string `json:"shipToAddressId"`
	Lines           []struct {
		LineNo        int    `json:"lineNo"`
		CatalogItemID string `json:"catalogItemId"`
		Quantity      int    `json:"quantity"`
	} `json:"lines"`
	Groups []struct {
		GroupNo     int    `json:"groupNo"`
		OfferID     string `json:"offerId"`
		Tier        string `json:"tier"`
		AmountMinor int64  `json:"amountMinor"`
	} `json:"groups"`
	Currency       string `json:"currency"`
	TotalMinor     int64  `json:"totalMinor"`
	QuoteExpiresAt string `json:"quoteExpiresAt"`
	Version        int64  `json:"version"`
}

type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		Session *wireSession `json:"session"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, sessions ports.SessionRepository) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(NewDeps(sessions))))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// send sends one message part and returns the agent's reply message.
func (w *wire) send(t *testing.T, msg *a2a.Message) *a2a.Message {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("SendMessage: %v (a rejection must be answered in-band, never a JSON-RPC error)", err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	return reply
}

func decodeReply(t *testing.T, reply *a2a.Message) (wireEnvelope, string) {
	t.Helper()
	data, ok := reply.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", reply.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := reply.Parts[1].Content.(a2a.Text)
	return env, string(text)
}

// call sends one part and returns the decoded envelope and the text summary.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	return decodeReply(t, w.send(t, a2a.NewMessage(a2a.MessageRoleUser, part)))
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != "/problems/"+slug {
		t.Fatalf("problem = %+v, want %d /problems/%s", env.Problem, status, slug)
	}
}

func TestWireCheckoutSessionStatusReturnsTheRESTShape(t *testing.T) {
	w := newWire(t, seededSessions(t))

	env, text := w.skill(t, skillCheckoutSessionStatus, map[string]any{"sessionId": "sess-1"})
	s := env.Data.Session
	if env.Problem != nil || s == nil || env.Skill != skillCheckoutSessionStatus {
		t.Fatalf("envelope = %+v", env)
	}
	assertSessionHeader(t, s)
	assertSessionBody(t, s)
	want := "sess-1: OPEN, 1 line(s) in 1 group(s), total 1430 USD (minor units), quote expires 2026-10-25T10:15:00Z"
	if text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

func assertSessionHeader(t *testing.T, s *wireSession) {
	t.Helper()
	if s.SessionID != "sess-1" || s.State != "OPEN" || s.CartID != "cart-1" || s.ShopperID != "shopper-1" || s.ShipToAddressID != "addr-1" ||
		s.Currency != "USD" || s.TotalMinor != 1430 || s.Version != 1 || s.QuoteExpiresAt != "2026-10-25T10:15:00Z" {
		t.Fatalf("session = %+v", s)
	}
}

func assertSessionBody(t *testing.T, s *wireSession) {
	t.Helper()
	if len(s.Lines) != 1 || s.Lines[0].CatalogItemID != "A" || s.Lines[0].Quantity != 2 {
		t.Fatalf("lines = %+v", s.Lines)
	}
	if len(s.Groups) != 1 || s.Groups[0].OfferID != "o1" || s.Groups[0].Tier != "NEXT_DAY" || s.Groups[0].AmountMinor != 1430 {
		t.Fatalf("groups = %+v", s.Groups)
	}
}

// The PSP token never leaves the service, on any part of the answer.
func TestWireAnswerNeverCarriesThePSPToken(t *testing.T) {
	w := newWire(t, seededSessions(t))
	reply := w.send(t, a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": map[string]any{"sessionId": "sess-1"}})))
	raw, _ := json.Marshal(reply)
	for _, banned := range []string{"paymentMethodToken", "pendingToken", "PaymentMethodToken"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("the answer mentions %q: %s", banned, raw)
		}
	}
}

// A Text part holding the same JSON is accepted, like a Data part.
func TestWireAcceptsATextPartHoldingTheSkillJSON(t *testing.T) {
	w := newWire(t, seededSessions(t))
	env, _ := w.call(t, a2a.NewTextPart(`{"skill":"checkout_session_status","arguments":{"sessionId":"sess-1"}}`))
	if env.Problem != nil || env.Data.Session == nil || env.Data.Session.SessionID != "sess-1" {
		t.Fatalf("envelope = %+v", env)
	}
}

// The reply is bound to the caller's contextId so a delegating saga can group
// its interactions.
func TestWireReplyCarriesTheCallersContextID(t *testing.T) {
	w := newWire(t, seededSessions(t))
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": map[string]any{"sessionId": "sess-1"}}))
	msg.ContextID = "ctx-42"
	reply := w.send(t, msg)
	if reply.ContextID != "ctx-42" || reply.Role != a2a.MessageRoleAgent {
		t.Fatalf("reply context %q role %q, want ctx-42 / agent", reply.ContextID, reply.Role)
	}
}

// Not-found is the REST classification (404 not-found) for an unknown id and
// for an id that cannot name a session.
func TestWireUnknownAndOverlongIdsAreNotFound(t *testing.T) {
	w := newWire(t, seededSessions(t))
	for _, id := range []string{"ghost", strings.Repeat("x", maxSessionIDLength+1)} {
		env, text := w.skill(t, skillCheckoutSessionStatus, map[string]any{"sessionId": id})
		wantProblem(t, env, 404, "not-found")
		if !strings.HasPrefix(text, "Not found (404): ") {
			t.Errorf("summary = %q", text)
		}
		if strings.Contains(env.Problem.Detail, "checkout session ghost") || strings.Contains(env.Problem.Detail, "usecases") {
			t.Errorf("the detail must be adapter-owned text, got %q", env.Problem.Detail)
		}
	}
	// 128 characters is the longest id that can name a session: a miss, not a bad request.
	env, _ := w.skill(t, skillCheckoutSessionStatus, map[string]any{"sessionId": strings.Repeat("x", maxSessionIDLength)})
	wantProblem(t, env, 404, "not-found")
}

// The argument validation table: every rejection is a 400 problem answered in
// the envelope (SendMessage never errors), and nothing is read.
func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, seededSessions(t))
	cases := []struct {
		name   string
		part   *a2a.Part
		slug   string
		detail string
		skill  string
	}{
		{"missing sessionId", a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": map[string]any{}}), "invalid-argument", "sessionId is required", skillCheckoutSessionStatus},
		{"no arguments at all", a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus}), "invalid-argument", "sessionId is required", skillCheckoutSessionStatus},
		{"blank sessionId", a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": map[string]any{"sessionId": "   "}}), "invalid-argument", "sessionId is required", skillCheckoutSessionStatus},
		{"unknown argument", a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": map[string]any{"sessionId": "sess-1", "sessionID2": "x"}}), "invalid-argument", "unknown field", skillCheckoutSessionStatus},
		{"wrong type", a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": map[string]any{"sessionId": 7}}), "invalid-argument", "cannot unmarshal", skillCheckoutSessionStatus},
		{"arguments not an object", a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": []any{"sess-1"}}), "invalid-argument", "cannot unmarshal", skillCheckoutSessionStatus},
		{"unknown skill", a2a.NewDataPart(map[string]any{"skill": "confirm_checkout", "arguments": map[string]any{"sessionId": "sess-1"}}), "unknown-skill", `"confirm_checkout" is not offered`, "confirm_checkout"},
		{"no skill field", a2a.NewDataPart(map[string]any{"arguments": map[string]any{"sessionId": "sess-1"}}), "malformed-skill-request", `no "skill" field`, ""},
		{"unknown envelope field", a2a.NewDataPart(map[string]any{"skill": skillCheckoutSessionStatus, "arguments": map[string]any{"sessionId": "sess-1"}, "extra": 1}), "malformed-skill-request", "unknown field", ""},
		{"trailing data in a text part", a2a.NewTextPart(`{"skill":"checkout_session_status","arguments":{"sessionId":"sess-1"}} {"x":1}`), "malformed-skill-request", "unexpected data after the JSON document", ""},
		{"text part that is not JSON", a2a.NewTextPart("where is my order"), "malformed-skill-request", "not valid", ""},
		{"control: whitespace in a text part is fine", a2a.NewTextPart(`{"skill":"checkout_session_status","arguments":{"sessionId":"sess-1"} }`), "", "", skillCheckoutSessionStatus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, text := w.call(t, tc.part)
			if tc.slug == "" { // the last case is the control: well-formed, answered with data
				if env.Problem != nil || env.Data.Session == nil {
					t.Fatalf("control case must succeed, got %+v", env)
				}
				return
			}
			wantProblem(t, env, 400, tc.slug)
			if env.Skill != tc.skill {
				t.Errorf("envelope skill = %q, want %q", env.Skill, tc.skill)
			}
			if !strings.Contains(env.Problem.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to contain %q", env.Problem.Detail, tc.detail)
			}
			if !strings.Contains(text, env.Problem.Title) {
				t.Errorf("summary %q lacks the problem title %q", text, env.Problem.Title)
			}
		})
	}
}

// A message with an unusable first part (a file part) is a malformed request,
// answered in-band; so is a part list the SDK would accept but the executor
// cannot read.
func TestWireNonJSONPartIsMalformed(t *testing.T) {
	w := newWire(t, seededSessions(t))
	env, _ := w.call(t, a2a.NewRawPart([]byte("binary")))
	wantProblem(t, env, 400, "malformed-skill-request")
}

// failingSessions is a repository whose reads fail with an infrastructure
// error that must never reach an agent client.
type failingSessions struct{ ports.SessionRepository }

func (failingSessions) Get(context.Context, cs.ID) (*cs.CheckoutSession, error) {
	return nil, errors.New("dial tcp 10.0.0.7:5432: password=hunter2 connection refused")
}

func TestWireUnexpectedFailureIsAnInternalErrorProblemWithoutLeaks(t *testing.T) {
	w := newWire(t, failingSessions{seededSessions(t)})
	env, text := w.skill(t, skillCheckoutSessionStatus, map[string]any{"sessionId": "sess-1"})
	wantProblem(t, env, 500, "internal-error")
	for _, leaked := range []string{"hunter2", "5432", "dial tcp"} {
		if strings.Contains(env.Problem.Detail, leaked) || strings.Contains(text, leaked) {
			t.Fatalf("infrastructure detail %q leaked: %+v / %q", leaked, env.Problem, text)
		}
	}
}

// Each skill run is one OTel span carrying a2a.skill, like mcp.tool.
func TestSkillRunEmitsASpanWithTheSkillAttribute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	w := newWire(t, seededSessions(t))
	w.skill(t, skillCheckoutSessionStatus, map[string]any{"sessionId": "sess-1"})

	for _, span := range recorder.Ended() {
		if span.Name() != "a2a.skill "+skillCheckoutSessionStatus {
			continue
		}
		for _, attr := range span.Attributes() {
			if string(attr.Key) == "a2a.skill" && attr.Value.AsString() == skillCheckoutSessionStatus {
				return
			}
		}
		t.Fatalf("span %q lacks the a2a.skill attribute: %v", span.Name(), span.Attributes())
	}
	t.Fatal("no a2a.skill span was recorded")
}

func TestSummarizeFlagsAnUnknownPlacementOutcome(t *testing.T) {
	got := summarize(sessionView{SessionID: "s", State: "PLACING", Currency: "USD", OrderID: ""})
	if !strings.Contains(got, "PLACING") || !strings.Contains(got, "outcome is unknown") {
		t.Fatalf("summary = %q", got)
	}
	if got := summarize(sessionView{SessionID: "s", State: "COMPLETED", OrderID: "ord-9"}); !strings.Contains(got, "order ord-9") || strings.Contains(got, "unknown") {
		t.Fatalf("summary = %q", got)
	}
}
