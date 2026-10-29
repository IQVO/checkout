// Package httpx is the tiny HTTP client the outbound gateways share: one
// bounded request, one bounded response, and the problem+json slug. Each
// gateway restates the one or two routes it uses with its own DTOs and decides
// what a status means; nothing here knows a sibling's schema.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout bounds one call; a slower sibling is unavailable.
const DefaultTimeout = 5 * time.Second

// maxResponseBytes bounds what the client reads of one response.
const maxResponseBytes = 1 << 20

// Client is a base URL and an http.Client.
type Client struct {
	base string
	http *http.Client
}

// New builds a Client for the base URL (no trailing slash needed). A nil
// client gets one with DefaultTimeout.
func New(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), http: client}
}

// Response is a fully read answer.
type Response struct {
	Status int
	Body   []byte
}

// OK reports a 2xx status.
func (r Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// ServerError reports a 5xx status.
func (r Response) ServerError() bool { return r.Status >= 500 }

// Slug returns the last path segment of the problem+json `type` of the body,
// empty when the body is not a problem document.
func (r Response) Slug() string {
	var p struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(r.Body, &p) != nil {
		return ""
	}
	return p.Type[strings.LastIndex(p.Type, "/")+1:]
}

// Detail returns the problem detail (or title) for error messages.
func (r Response) Detail() string {
	var p struct{ Title, Detail string }
	_ = json.Unmarshal(r.Body, &p)
	if p.Detail != "" {
		return p.Detail
	}
	return p.Title
}

// Do sends one request. body may be nil; key, when not empty, is sent as the
// Idempotency-Key. A transport error or an unreadable body is returned as err
// (the caller maps it to the sibling's "unavailable" sentinel).
func (c *Client) Do(ctx context.Context, method, path, key string, body []byte) (Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return Response{}, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, application/problem+json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}
	return Response{Status: resp.StatusCode, Body: payload}, nil
}
