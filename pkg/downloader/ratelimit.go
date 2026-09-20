package downloader

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ParseRateString converts a human-readable bandwidth string (e.g. "2mb",
// "500kb", "10mb") to bytes per second. Returns <= 0 for unlimited.
// Accepts "0", "-1", "unlimited", and empty as unlimited.
func ParseRateString(s string) (int64, error) {
	trimmed := strings.TrimSpace(strings.ToLower(s))
	if trimmed == "" || trimmed == "0" || trimmed == "unlimited" || trimmed == "-1" {
		return 0, nil
	}
	var multiplier int64 = 1
	switch {
	case strings.HasSuffix(trimmed, "gb"):
		multiplier = 1 << 30
		trimmed = strings.TrimSuffix(trimmed, "gb")
	case strings.HasSuffix(trimmed, "g"):
		multiplier = 1 << 30
		trimmed = strings.TrimSuffix(trimmed, "g")
	case strings.HasSuffix(trimmed, "mb"):
		multiplier = 1 << 20
		trimmed = strings.TrimSuffix(trimmed, "mb")
	case strings.HasSuffix(trimmed, "m"):
		multiplier = 1 << 20
		trimmed = strings.TrimSuffix(trimmed, "m")
	case strings.HasSuffix(trimmed, "kb"):
		multiplier = 1 << 10
		trimmed = strings.TrimSuffix(trimmed, "kb")
	case strings.HasSuffix(trimmed, "k"):
		multiplier = 1 << 10
		trimmed = strings.TrimSuffix(trimmed, "k")
	case strings.HasSuffix(trimmed, "b"):
		multiplier = 1
		trimmed = strings.TrimSuffix(trimmed, "b")
	}
	val, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as a number", trimmed)
	}
	if val <= 0 {
		return 0, nil
	}
	return val * multiplier, nil
}

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
	newRate := float64(bytesPerSec)
	if newRate <= 0 {
		d.rate = 0
		d.tokens = 0
		d.lastTime = time.Time{}
		return
	}
	// Refill with the old rate before switching so a rate change does not
	// conjure or destroy budget, then cap the burst to the new rate.
	if !d.lastTime.IsZero() && d.rate > 0 {
		elapsed := time.Since(d.lastTime).Seconds()
		if elapsed > 0 {
			d.tokens += elapsed * d.rate
		}
	}
	d.rate = newRate
	if d.tokens > d.rate {
		d.tokens = d.rate
	}
	if d.lastTime.IsZero() {
		// Leave lastTime zero so the next Wait starts fully charged;
		// do not pre-fill here or an idle limiter would double-count.
	} else {
		d.lastTime = time.Now()
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
//
// The bucket reserves budget up-front (tokens may go negative) so concurrent
// readers share the same debt instead of each spending the same future
// budget. Waits proceed in short slices so a rate change, removal of the
// limit, or ctx cancellation is observed promptly even for multi-second
// waits.
func (d *DynamicRateLimiter) WaitN(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	if d == nil {
		return nil
	}
	need := float64(n)

	d.mu.Lock()
	if d.rate <= 0 {
		d.mu.Unlock()
		return nil
	}
	now := time.Now()
	if d.lastTime.IsZero() {
		d.lastTime = now
		d.tokens = d.rate // start fully charged (1s burst)
	} else {
		elapsed := now.Sub(d.lastTime).Seconds()
		if elapsed > 0 {
			d.tokens += elapsed * d.rate
			if d.tokens > d.rate {
				d.tokens = d.rate
			}
			d.lastTime = now
		}
	}
	// Reserve the budget now; concurrent waiters see the debt.
	d.tokens -= need
	if d.tokens >= 0 {
		d.lastTime = time.Now()
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	// Wait until the reserved debt is repaid by refills. Each iteration
	// refills under lock, so rate changes and removals take effect
	// immediately and concurrent readers accumulate correctly.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		d.mu.Lock()
		rate := d.rate
		if rate <= 0 {
			// Limit removed while waiting: debt is forgiven.
			if d.tokens < 0 {
				d.tokens = 0
			}
			d.mu.Unlock()
			return nil
		}
		now := time.Now()
		elapsed := now.Sub(d.lastTime).Seconds()
		if elapsed > 0 {
			d.tokens += elapsed * rate
			d.lastTime = now
		}
		if d.tokens >= 0 {
			d.mu.Unlock()
			return nil
		}
		// Sleep only until the debt clears or a short slice, whichever
		// is smaller, so cancellation and rate changes stay responsive.
		debt := -d.tokens
		waitDur := time.Duration(debt / rate * float64(time.Second))
		const maxSlice = 100 * time.Millisecond
		if waitDur > maxSlice {
			waitDur = maxSlice
		}
		if waitDur < 5*time.Millisecond {
			waitDur = 5 * time.Millisecond
		}
		d.mu.Unlock()

		timer := time.NewTimer(waitDur)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
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
