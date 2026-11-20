package http

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// maxBodyBytes bounds a request body; every body here is a few KiB.
const maxBodyBytes = 64 << 10

type statusBody struct {
	Status string `json:"status"`
}

// problemDetail is the RFC 9457 application/problem+json body.
type problemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON strictly decodes r's body into dest: unknown fields, trailing
// data, an empty body and malformed JSON are errMalformedRequest.
func decodeJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("%w: %w", errMalformedRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after the JSON body", errMalformedRequest)
	}
	return nil
}

// writeError writes err as its RFC 9457 problem. An unmapped error is a 500
// whose detail never leaks the internal message (it is logged instead). The
// replay class of the problem reaches the Idempotency-Key middleware through
// writeProblem.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	detail := err.Error()
	if p == internalProblem {
		slog.ErrorContext(r.Context(), "request failed", "error", sanitizeLog(err.Error()), "method", r.Method, "path", sanitizeLog(r.URL.Path))
		detail = "an unexpected error occurred"
	}
	writeProblem(w, r, p, detail)
}

// replayMarker is implemented by the capturing writer of the Idempotency-Key
// middleware: the error writer tells it the replay class of the answer, and the
// middleware decides from that what to do with it.
type replayMarker interface{ MarkReplay(replayClass) }

// writeProblem writes p with an explicit detail and marks the capturing writer
// (if any) with the replay class of p. Every problem leaves through here, so a
// problem cannot be answered without its class being declared.
func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	if m, ok := w.(replayMarker); ok {
		m.MarkReplay(p.class)
	}
	writeBody(w, p.status, problemBody(r, p, detail))
}

func problemBody(r *http.Request, p problem, detail string) problemDetail {
	return problemDetail{Type: problemBase + p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath()}
}

func writeBody(w http.ResponseWriter, status int, body problemDetail) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
