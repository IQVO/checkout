// Package ids is the production ports.IDGenerator: a session id is "sess-" and
// 12 random hexadecimal characters.
package ids

import (
	"strings"

	"github.com/google/uuid"

	"github.com/IQVO/checkout/internal/application/ports"
)

// Generator mints ids.
type Generator struct{}

var _ ports.IDGenerator = Generator{}

// NewSessionID returns "sess-" followed by 12 random hexadecimal characters.
func (Generator) NewSessionID() string {
	return "sess-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}
