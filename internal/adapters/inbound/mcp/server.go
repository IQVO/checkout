// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// SAME application-layer read use case the REST adapter uses (GetSession). It
// is built on the official MCP Go SDK and served over Streamable HTTP only.
//
// The surface is READ-ONLY (docs/adr/0005-mcp-server.md): the one tool reads a
// checkout session; opening and confirming a session stay on REST. A purchase
// moves money, so no write verb is reachable here (TestMCPServerIsReadOnly).
// This package depends inward on the application layer and the domain only,
// never on an outbound adapter or the REST adapter, and nothing else may depend
// on it (internal/architecture's TestMCPAdapterDependencyRule). There is no
// auth of any kind (fleet-wide revert 2026-09-11).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "checkout-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Checkout of the retail storefront (read-only): read a checkout session with " +
	"get_checkout_session (its state OPEN, AUTHORIZING, DECLINED, PLACING, REQUOTE_REQUIRED, COMPLETED, EXPIRED or " +
	"FAILED, the numbered lines and shipment groups with their allocation, the binding total and when its quote " +
	"expires, the payment authorization and the placed order, and the re-quoted offers of a REQUOTE_REQUIRED " +
	"session). A session in PLACING means the place-order outcome is unknown and a resolver is replaying it. " +
	"Nothing here changes data: opening and confirming a session are REST operations of checkout."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
