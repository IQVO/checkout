package mcp

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/IQVO/checkout/internal/application/ports"
)

// toolError builds a tool-level error "<slug>: <detail>". Returned from a
// handler it becomes an isError tool result, never a transport failure. The
// slug is the one the REST adapter uses as the last segment of its RFC 9457
// "type" URI, so a client sees one error vocabulary over both surfaces.
func toolError(slug, detail string) error {
	return fmt.Errorf("%s: %s", slug, detail)
}

// mapError turns a use-case error into the tool error the model sees. The one
// read can only fail by not finding the session (ports.ErrNotFound); anything
// else is logged and reported generically so infrastructure details (DSNs,
// SQL) never reach the model.
func mapError(err error) error {
	if errors.Is(err, ports.ErrNotFound) {
		return toolError("not-found", err.Error())
	}
	slog.Error("mcp tool failed with an unexpected error", "error", err)
	return toolError("internal-error", "an unexpected internal error occurred")
}
