package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/httpx"
)

func TestDoSendsKeyAndBodyAndReadsTheAnswer(t *testing.T) {
	var gotKey, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		gotKey, gotCT, gotBody = r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type"), string(b[:n])
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"/problems/book-closed","title":"T","detail":"D"}`))
	}))
	defer srv.Close()
	resp, err := httpx.New(srv.URL+"/", nil).Do(context.Background(), http.MethodPost, "/x", "k1", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "k1" || gotCT != "application/json" || gotBody != `{"a":1}` {
		t.Fatalf("request: %q %q %q", gotKey, gotCT, gotBody)
	}
	if resp.Status != 409 || resp.OK() || resp.ServerError() || resp.Slug() != "book-closed" || resp.Detail() != "D" {
		t.Fatalf("response %+v slug %q", resp, resp.Slug())
	}
}

func TestResponseHelpers(t *testing.T) {
	if !(httpx.Response{Status: 201}).OK() || !(httpx.Response{Status: 503}).ServerError() {
		t.Fatal("status helpers")
	}
	r := httpx.Response{Status: 400, Body: []byte(`not json`)}
	if r.Slug() != "" || r.Detail() != "" {
		t.Fatal("a non-problem body has no slug or detail")
	}
	if (httpx.Response{Body: []byte(`{"title":"only title"}`)}).Detail() != "only title" {
		t.Fatal("title fallback")
	}
}

func TestDoTransportErrorAndBadURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := httpx.New(url, nil).Do(context.Background(), http.MethodGet, "/x", "", nil); err == nil {
		t.Fatal("a closed server is a transport error")
	}
	if _, err := httpx.New(url, nil).Do(context.Background(), "BAD METHOD", "/x", "", nil); err == nil {
		t.Fatal("a bad method cannot build a request")
	}
}
