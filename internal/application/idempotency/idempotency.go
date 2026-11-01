// Package idempotency holds the stored response of an Idempotency-Key.
package idempotency

import "time"

// TTL is how long a key is remembered (D3).
const TTL = 24 * time.Hour

// Record is the first response stored under an Idempotency-Key. RequestHash is
// the fingerprint of the request that produced it: sha256 over the method, the
// path and the sha256 of the body.
type Record struct {
	RequestHash string
	Status      int
	ContentType string
	Body        []byte
	CreatedAt   time.Time
}
