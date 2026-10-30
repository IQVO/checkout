// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter over the
// SAME application-layer read use case the REST and MCP adapters use
// (GetSession). It is built on the official A2A Go SDK (a2aproject/a2a-go) and
// speaks JSON-RPC 2.0 over HTTP, with the Agent Card served at the well-known
// path.
//
// Per ADR 0007, A2A is the DELEGATION channel between bounded contexts: an
// agent client sends a goal-shaped skill request as a structured message part
// and this agent answers from its own state through its own use cases. It is
// not a data-sync channel: facts between contexts still travel exclusively as
// CloudEvents on Kafka (ADR 0004), published through the transactional outbox.
//
// This agent is READ-ONLY, exactly like the MCP adapter (ADR 0005): its one
// skill reads a checkout session. Opening and confirming a session move a
// shopper's money and stay on REST; this package never writes, never touches
// the outbox, never calls another bounded context and contains no outbound A2A
// client (TestA2AAdapterIsReadOnlyAndHasNoOutboundClient).
//
// Like the MCP adapter, this package depends inward on the application layer
// and the domain only, never on an outbound adapter or another inbound
// adapter, and nothing else may depend on it (TestA2AAdapterDependencyRule).
// The composition root (cmd/a2a) wires the concrete repository into the use
// case. Domain structs never leak across the message boundary. There is no
// auth of any kind (fleet-wide revert 2026-09-11).
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skill needs, injected by the composition root. It
// carries the SAME read use case the REST and MCP adapters use and no write
// use case at all.
type Deps struct {
	// GetSession is the existing read use case, reused unchanged.
	GetSession *usecases.GetSession
}

// NewDeps wires the read use case over the one read repository it needs.
// Handing the repository over this way keeps cmd/a2a from ever naming the
// shared usecases.Deps bundle, which also carries the write seams (the outbox,
// the sibling gateways) no A2A binary may reach.
func NewDeps(sessions ports.SessionRepository) Deps {
	return Deps{GetSession: &usecases.GetSession{Deps: usecases.Deps{Sessions: sessions}}}
}

// Card builds this agent's Agent Card (ADR 0007). The surface is ONE curated,
// goal-shaped READ skill derived from the MCP tool get_checkout_session (ADR
// 0005): this context's MCP charter is read-only (a purchase moves money), so
// the agent is read-only too and never widens what the context exposes. The
// card declares no security schemes on purpose: this surface is
// unauthenticated by the same fleet-wide decision as REST and MCP; schemes are
// added here, not in a redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "checkout",
		Description: "Checkout agent for the retail storefront (read-only): answers where a checkout session stands, from its state (OPEN, AUTHORIZING, DECLINED, PLACING, REQUOTE_REQUIRED, COMPLETED, EXPIRED or FAILED) to the numbered lines and shipment groups, the binding total and when its quote expires, the payment authorization and the placed order. It never opens, confirms or changes a session: those move a shopper's money and stay on the REST API.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/checkout",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillCheckoutSessionStatus,
				Name:        "Where does a checkout session stand",
				Description: "Read one checkout session by its sessionId: state, cart and shopper references, numbered lines, shipment groups (groupNo, offerId, tier, lineNos, amountMinor), the binding total and currency, when the price quote expires, the payment authorizationId, the placed orderId, the re-quoted offers of a REQUOTE_REQUIRED session and the failureCode of a FAILED one. PLACING means the place-order outcome is unknown and a resolver is replaying it: ask again later. Answers a 404 problem for an unknown or malformed sessionId. Persists nothing.",
				Tags:        []string{"checkout", "session", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"checkout_session_status","arguments":{"sessionId":"sess-3f9c1a2b4d5e"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): the one curated skill is a transactional request/response read, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (ADR 0007), mirroring ADR 0005's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
