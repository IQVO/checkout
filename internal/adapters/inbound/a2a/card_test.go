package a2a

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const testAgentURL = "http://checkout-a2a.warehouse-systems.svc.cluster.local:8093/"

func testCard() *a2a.AgentCard { return Card(Config{AgentURL: testAgentURL, Version: "1.0.0"}) }

// TestCardShape pins the identity, interface and capability surface of the
// Agent Card: the discovery contract other contexts program against.
func TestCardShape(t *testing.T) {
	card := testCard()

	if card.Name != "checkout" {
		t.Errorf("card name = %q, want checkout", card.Name)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("card has %d interfaces, want 1", len(card.SupportedInterfaces))
	}
	iface := card.SupportedInterfaces[0]
	if iface.URL != testAgentURL || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("interface = %+v, want JSONRPC at the configured agent URL", iface)
	}
	if card.Capabilities.Streaming {
		t.Error("streaming must not be advertised: this is a message-only agent")
	}
	if card.SecuritySchemes != nil {
		t.Error("card must declare no security schemes: the surface is unauthenticated fleet-wide")
	}
	if card.Provider == nil || card.Provider.Org != "IQVO" {
		t.Errorf("provider = %+v, want IQVO", card.Provider)
	}
}

// TestCardSkillIsCuratedAndTruthful pins the one-skill, READ-ONLY surface and
// proves the card never lies about its own contract: every advertised example
// must parse under the SAME strict decoder the executor uses, name its own
// skill, and carry arguments the skill accepts.
func TestCardSkillIsCuratedAndTruthful(t *testing.T) {
	card := testCard()

	if len(card.Skills) != 1 || card.Skills[0].ID != skillCheckoutSessionStatus {
		t.Fatalf("card skills = %+v, want exactly %s (the MCP surface has one read)", card.Skills, skillCheckoutSessionStatus)
	}
	s := card.Skills[0]
	if s.Name == "" || s.Description == "" || len(s.Examples) == 0 {
		t.Errorf("skill %q lacks a name, description or example", s.ID)
	}
	if !containsTag(s.Tags, "read") || containsTag(s.Tags, "write") {
		t.Errorf("skill %q must be tagged read and never write (this context's MCP surface is read-only), tags = %v", s.ID, s.Tags)
	}
	for _, ex := range s.Examples {
		var req skillRequest
		if err := decodeStrict([]byte(ex), &req); err != nil {
			t.Errorf("example is not valid under the strict decoder: %v", err)
			continue
		}
		if req.Skill != s.ID {
			t.Errorf("example names skill %q, want %q", req.Skill, s.ID)
		}
		if err := decodeStrict(req.Arguments, &checkoutSessionStatusArgs{}); err != nil {
			t.Errorf("example arguments are rejected by the skill's own strict decoder: %v", err)
		}
	}
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// TestCardNeverClaimsToWrite keeps the card honest about the read-only charter:
// no skill id may look like a write, and the description says read-only.
func TestCardNeverClaimsToWrite(t *testing.T) {
	card := testCard()
	for _, s := range card.Skills {
		for _, verb := range []string{"open_", "confirm_", "place_", "void_", "authorize_", "capture_", "create_", "update_", "cancel_"} {
			if strings.HasPrefix(s.ID, verb) {
				t.Errorf("skill %q looks like a write; this agent is read-only", s.ID)
			}
		}
	}
	if !strings.Contains(card.Description, "read-only") {
		t.Errorf("the card description must say the agent is read-only: %q", card.Description)
	}
}

// TestCardSerializable proves the card marshals to JSON a client can parse
// back, with the discovery fields intact.
func TestCardSerializable(t *testing.T) {
	raw, err := json.Marshal(testCard())
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var back a2a.AgentCard
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("card JSON does not round-trip: %v", err)
	}
	if back.Name != "checkout" || len(back.Skills) != 1 {
		t.Errorf("round-tripped card = %q with %d skills", back.Name, len(back.Skills))
	}
}
