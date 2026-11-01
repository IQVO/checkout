package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/IQVO/checkout/internal/application/idempotency"
	"github.com/IQVO/checkout/internal/application/ports"
)

// IdempotencyKeyHeader is the request header a caller must supply on a route
// wrapped by RequireIdempotencyKey.
const IdempotencyKeyHeader = "Idempotency-Key"

// ReplayedHeader marks a response that was served from the stored record. It
// is set on every replay and only on a replay.
const ReplayedHeader = "Idempotency-Replayed"

// maxKeyLength bounds an Idempotency-Key.
const maxKeyLength = 128

// RequireIdempotencyKey is the Idempotency-Key middleware of every POST route
// (REST API guideline section 2). The key is global to the service: the stored
// record is looked up by the key alone and compared by fingerprint (method +
// path + sha256 of the body). The first response below 500 for a key is stored
// for 24 hours (idempotency.TTL); the same key and fingerprint replays it with
// Idempotency-Replayed: true, the same key with another fingerprint is 409
// idempotency-key-reused. A 5xx is never stored, so the key can retry the real
// work. Requests that carry the same key are serialised inside the process, so
// N concurrent identical requests run the command once and the others replay
// its answer; across replicas the store keeps the first record and the loser
// replays it.
//
// Build ONE middleware for the router and share it between the routes: the key
// locks live in the returned closure.
func RequireIdempotencyKey(store ports.IdempotencyStore, clock ports.Clock) func(http.Handler) http.Handler {
	locks := &keyLocks{held: map[string]*keyLock{}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyKeyHeader)
			if key == "" {
				writeProblem(w, r, idempotencyKeyRequired, "send an Idempotency-Key header of 1 to 128 printable ASCII characters, unique per logical request")
				return
			}
			if !validKey(key) {
				writeProblem(w, r, idempotencyKeyInvalid, "the Idempotency-Key must be 1 to 128 printable ASCII characters")
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
			_ = r.Body.Close()
			if err != nil || len(body) > maxBodyBytes {
				writeError(w, r, errMalformedRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			defer locks.lock(key)()
			serve(w, r, next, store, clock, key, requestHash(r, body))
		})
	}
}

// serve replays the stored record of key, rejects a reuse, or runs next and
// stores its answer.
func serve(w http.ResponseWriter, r *http.Request, next http.Handler, store ports.IdempotencyStore, clock ports.Clock, key, hash string) {
	rec, err := store.Get(r.Context(), key)
	switch {
	case err == nil:
		replay(w, r, rec, hash)
		return
	case !errors.Is(err, ports.ErrNotFound):
		writeError(w, r, err)
		return
	}
	out := &capture{header: http.Header{}}
	next.ServeHTTP(out, r)
	if out.status() < http.StatusInternalServerError {
		stored := idempotency.Record{
			RequestHash: hash, Status: out.status(), ContentType: out.header.Get("Content-Type"),
			Body: out.body.Bytes(), CreatedAt: clock.Now().UTC().Truncate(time.Microsecond),
		}
		won, err := store.Put(r.Context(), key, stored)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !won {
			// Another replica stored a record for this key first: answer with it.
			if first, err := store.Get(r.Context(), key); err == nil {
				replay(w, r, first, hash)
				return
			}
		}
	}
	out.flushTo(w)
}

// validKey reports whether key is 1 to 128 printable ASCII characters.
func validKey(key string) bool {
	if len(key) == 0 || len(key) > maxKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// requestHash is the request fingerprint: sha256 over the method, the path and
// the sha256 of the body.
func requestHash(r *http.Request, body []byte) string {
	sum := sha256.Sum256(body)
	h := sha256.New()
	h.Write([]byte(r.Method + " " + r.URL.EscapedPath() + "\n"))
	h.Write([]byte(hex.EncodeToString(sum[:])))
	return hex.EncodeToString(h.Sum(nil))
}

func replay(w http.ResponseWriter, r *http.Request, rec idempotency.Record, hash string) {
	if rec.RequestHash != hash {
		writeProblem(w, r, idempotencyKeyReused, "the Idempotency-Key was used with a different request")
		return
	}
	if rec.ContentType != "" {
		w.Header().Set("Content-Type", rec.ContentType)
	}
	w.Header().Set(ReplayedHeader, "true")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}

// keyLocks serialises the requests that carry the same Idempotency-Key.
type keyLocks struct {
	mu   sync.Mutex
	held map[string]*keyLock
}

type keyLock struct {
	sync.Mutex
	users int
}

// lock blocks until the caller owns key and returns the release func.
func (k *keyLocks) lock(key string) func() {
	k.mu.Lock()
	l := k.held[key]
	if l == nil {
		l = &keyLock{}
		k.held[key] = l
	}
	l.users++
	k.mu.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		k.mu.Lock()
		if l.users--; l.users == 0 {
			delete(k.held, key)
		}
		k.mu.Unlock()
	}
}

// capture records the handler's response so it can be stored before anything
// reaches the client.
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

func (c *capture) status() int {
	if c.code == 0 {
		return http.StatusOK
	}
	return c.code
}

func (c *capture) flushTo(w http.ResponseWriter) {
	for k, vs := range c.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(c.status())
	_, _ = w.Write(c.body.Bytes())
}
