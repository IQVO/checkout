package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/IQVO/checkout/internal/application/ports"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/IQVO/checkout/internal/adapters/inbound/a2a"

// skillCheckoutSessionStatus is the one skill advertised on the Agent Card. It
// is the goal-shaped form of the MCP tool get_checkout_session.
const skillCheckoutSessionStatus = "checkout_session_status"

// maxSessionIDLength mirrors the REST and MCP {sessionId} guard: an id that
// cannot name a session is a miss, never a server error.
const maxSessionIDLength = 128

// skillRequest is the wire contract of every skill call: the first meaningful
// message part carries (as a Data part, or a Text part holding the same JSON)
// the skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored. (Go's
// encoding/json matches field names case-insensitively, so "sessionid" still
// reaches sessionId; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// checkoutSessionStatusArgs is the skill's argument shape.
type checkoutSessionStatusArgs struct {
	SessionID string `json:"sessionId"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0007).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillCheckoutSessionStatus:
		data, problem := deps.checkoutSessionStatus(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{Session: &data, summary: summarize(data)}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first
// meaningful part. A Data part (or a Text part holding the same JSON object)
// is accepted; anything else is a request-shape problem, answered, not
// raised, because a malformed delegation is a business answer for the calling
// agent, not a transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// checkoutSessionStatus is the read skill, through the same GetSession use
// case the REST and MCP adapters use. A missing sessionId is a bad request; a
// longer-than-possible one is a miss, exactly as on REST ({sessionId} cannot
// be empty there, and an over-long one is a 404).
func (d Deps) checkoutSessionStatus(ctx context.Context, rawArgs json.RawMessage) (sessionView, *problem) {
	var args checkoutSessionStatusArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return sessionView{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	if strings.TrimSpace(args.SessionID) == "" {
		return sessionView{}, problemInvalidArgument("sessionId is required")
	}
	if len(args.SessionID) > maxSessionIDLength {
		return sessionView{}, problemNotFound()
	}
	view, err := d.GetSession.Execute(ctx, args.SessionID)
	if err != nil {
		return sessionView{}, problemFromUseCase(ctx, err)
	}
	return toSession(view), nil
}

func problemNotFound() *problem {
	return &problem{Type: problemTypeBase + "not-found", Title: "Not found", Detail: "no checkout session exists with this sessionId", Status: 404}
}

// problemFromUseCase maps a use case error to a problem answer, using the SAME
// classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a read can raise: a missing session is 404
// not-found and anything unrecognised is 500 internal-error. (The REST
// adapter's 409, 422, 502 and 503 classes belong to writes and sibling calls
// this read-only agent does not make.) The detail is adapter-owned text, so a
// use case's wording or an infrastructure error never reaches an agent client:
// the unexpected error is logged, with the trace id, instead.
func problemFromUseCase(ctx context.Context, err error) *problem {
	if errors.Is(err, ports.ErrNotFound) {
		return problemNotFound()
	}
	slog.ErrorContext(ctx, "a2a skill failed with an unexpected error", "error", err)
	return &problem{Type: problemTypeBase + "internal-error", Title: "Internal server error", Detail: "an unexpected error occurred; see the server logs", Status: 500}
}
