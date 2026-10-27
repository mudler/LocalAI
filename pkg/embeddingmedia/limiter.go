// SPDX-License-Identifier: MIT
package embeddingmedia

import "sync"

// Limiter bounds concurrent media preparations of one frontend process.
// State is deliberately per frontend: several frontends multiply capacity.
type Limiter struct {
	mu       sync.Mutex
	capacity int
	held     int
}

// NewLimiter exists for tests and for NewPreparationLimiter. Production code
// must use NewPreparationLimiter so capacity is not caller configurable.
func NewLimiter(capacity int) *Limiter {
	if capacity < 0 {
		capacity = 0
	}
	return &Limiter{capacity: capacity}
}

// NewPreparationLimiter returns a limiter with the fixed protocol capacity.
func NewPreparationLimiter() *Limiter { return NewLimiter(int(MaxConcurrentPreparations)) }

var sharedPreparation = NewPreparationLimiter()

// SharedPreparationLimiter returns the process-wide limiter that the HTTP routes
// hand to every endpoint that prepares media, so embeddings and rerank draw on
// the same two slots instead of two each.
func SharedPreparationLimiter() *Limiter { return sharedPreparation }

// TryAcquire admits one holder without waiting, or fails with a typed 429.
// The returned release function is idempotent and frees exactly one slot.
func (l *Limiter) TryAcquire() (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held >= l.capacity {
		return nil, &Error{Status: 429, Code: "embedding_capacity_exhausted"}
	}
	l.held++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.held--
			l.mu.Unlock()
		})
	}, nil
}
