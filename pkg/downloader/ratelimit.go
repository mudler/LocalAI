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

// Unlimited reports whether no throttling applies. A nil limiter or a rate
// <= 0 means unlimited.
func (d *DynamicRateLimiter) Unlimited() bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rate <= 0
}

// WaitN blocks until n bytes of budget are available, honouring ctx
// cancellation. It returns nil immediately when unlimited.
func (d *DynamicRateLimiter) WaitN(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	if d == nil {
		return nil
	}
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

	need := float64(n)
	if d.tokens >= need {
		d.tokens -= need
		d.lastTime = now
		d.mu.Unlock()
		return nil
	}

	// How long until we have enough tokens? Cap single waits at ~1s so
	// pause/cancel stays responsive even for large buffers.
	waitDur := time.Duration((need - d.tokens) / rate * float64(time.Second))
	if waitDur > time.Second {
		waitDur = time.Second
	}
	d.tokens = 0
	d.lastTime = now
	d.mu.Unlock()

	timer := time.NewTimer(waitDur)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
// Wait blocks until one byte of budget is available. Kept for backward
// compatibility; new code should prefer WaitN with the real buffer size.
func (d *DynamicRateLimiter) Wait(ctx context.Context) error {
	return d.WaitN(ctx, 1)
}

// rateLimitedReader wraps an io.ReadCloser with a DynamicRateLimiter so that
// reads respect the configured byte-per-second rate. The request context is
// honoured so pause/cancel stays responsive.
type rateLimitedReader struct {
	inner io.ReadCloser
	rl    *DynamicRateLimiter
	ctx   context.Context
}

func newRateLimitedReader(inner io.ReadCloser, rl *DynamicRateLimiter, ctx context.Context) io.ReadCloser {
	if ctx == nil {
		ctx = context.Background()
	}
	return &rateLimitedReader{inner: inner, rl: rl, ctx: ctx}
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Fast path: no limiter or unlimited rate means a single direct read.
	// This keeps multi-GB downloads at full speed when no limit is set.
	if r.rl == nil || r.rl.Unlimited() {
		return r.inner.Read(p)
	}
	// Throttle in chunks (max 32KB per wait) so large buffers cannot burst
	// past the budget and pause/cancel stays responsive.
	const maxChunk = 32 * 1024
	n := len(p)
	if n > maxChunk {
		n = maxChunk
	}
	if err := r.rl.WaitN(r.ctx, n); err != nil {
		return 0, err
	}
	return r.inner.Read(p[:n])
}

func (r *rateLimitedReader) Close() error {
	return r.inner.Close()
}

type dlCtxKey string

const ctxKeyRateLimiter dlCtxKey = "rate_limiter"

// ContextWithRateLimiter attaches a DynamicRateLimiter to ctx so
// DownloadFileWithContext can throttle the download speed.
func ContextWithRateLimiter(ctx context.Context, rl *DynamicRateLimiter) context.Context {
	return context.WithValue(ctx, ctxKeyRateLimiter, rl)
}

// RateLimiterFromContext returns the DynamicRateLimiter attached to ctx, or
// nil if none is set.
func RateLimiterFromContext(ctx context.Context) *DynamicRateLimiter {
	if rl, ok := ctx.Value(ctxKeyRateLimiter).(*DynamicRateLimiter); ok {
		return rl
	}
	return nil
}
