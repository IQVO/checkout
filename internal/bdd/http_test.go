package bdd

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// send performs one request against the router and records the answer.
func (w *world) send(method, path, key, body string) error {
	req, err := http.NewRequest(method, w.api().URL+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	w.status, w.body, w.header = resp.StatusCode, string(b), resp.Header
	return nil
}

// post sends a POST with a fresh Idempotency-Key.
func (w *world) post(path, body string) error {
	w.keys++
	return w.send(http.MethodPost, path, fmt.Sprintf("key-%d", w.keys), body)
}

func (w *world) get(path string) error { return w.send(http.MethodGet, path, "", "") }
