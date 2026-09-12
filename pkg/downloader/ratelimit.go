package downloader

import (
	"context"
	"io"
	"sync"
	"time"
)

// DynamicRateLimiter implements a token-bucket rate limiter whose rate can
// be changed at runtime. A zero-value limiter is unlimited (no waiting).
// All methods are safe for concurrent use.
type DynamicRateLimiter struct {
	mu       sync.Mutex
	rate     float64 // bytes per second; 0 means unlimited
	tokens   float64
	lastTime time.Time
}

// SetRate changes the target rate in bytes per second. A value <= 0 means
// unlimited (the Wait method becomes a no-op).
func (d *DynamicRateLimiter) SetRate(bytesPerSec int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rate = float64(bytesPerSec)
	if d.rate <= 0 {
		d.tokens = 0
		d.lastTime = time.Time{}
	}
}

// Wait blocks until a token is available for one byte, honouring ctx
// cancellation. It returns nil immediately when the rate is unlimited or
// when the context is already done.
func (d *DynamicRateLimiter) Wait(ctx context.Context) error {
	d.mu.Lock()
	rate := d.rate
	if rate <= 0 {
		d.mu.Unlock()
		return nil
	}

	now := time.Now()
	if d.lastTime.IsZero() {
		d.lastTime = now
		d.tokens = rate // start fully charged
	}

	// Refill tokens based on elapsed time since last call.
	elapsed := now.Sub(d.lastTime).Seconds()
	d.tokens += elapsed * rate
	if d.tokens > rate {
		d.tokens = rate
	}

	if d.tokens >= 1 {
		d.tokens--
		d.lastTime = now
		d.mu.Unlock()
		return nil
	}

	// How long until we have at least one token?
	waitDur := time.Duration((1 - d.tokens) / rate * float64(time.Second))
	d.lastTime = now
	d.tokens = 0
	d.mu.Unlock()

	select {
	case <-time.After(waitDur):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// rateLimitedReader wraps an io.ReadCloser with a DynamicRateLimiter so that
// reads respect the configured byte-per-second rate.
type rateLimitedReader struct {
	inner io.ReadCloser
	rl    *DynamicRateLimiter
}

func newRateLimitedReader(inner io.ReadCloser, rl *DynamicRateLimiter) io.ReadCloser {
	return &rateLimitedReader{inner: inner, rl: rl}
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	if r.rl == nil {
		return r.inner.Read(p)
	}
	// Throttle byte-by-byte so bursty reads don't exceed the budget.
	// An alternative would be to release all n bytes at once, but that
	// would allow a large burst up to the buffer size.
	for i := 0; i < len(p); i++ {
		if err := r.rl.Wait(context.Background()); err != nil {
			return i, err
		}
		n, err := r.inner.Read(p[i : i+1])
		if n == 0 {
			return i, err
		}
	}
	return len(p), nil
}

func (r *rateLimitedReader) Close() error {
	return r.inner.Close()
}
