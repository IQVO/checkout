package ids

import (
	"regexp"
	"testing"

	"github.com/IQVO/checkout/internal/domain/shared"
)

func TestNewSessionIDIsAValidUniqueID(t *testing.T) {
	rx := regexp.MustCompile(`^sess-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := Generator{}.NewSessionID()
		if !rx.MatchString(id) || !shared.ValidID(id) || seen[id] {
			t.Fatalf("id %q is malformed or repeated", id)
		}
		seen[id] = true
	}
}
