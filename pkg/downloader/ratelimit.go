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

// reservation is one queued WaitN request. Refills are assigned head-first,
// so an earlier reservation always matures before a later one regardless of
// size; a later reservation can never postpone an earlier reader.
type reservation struct {
	remaining float64
	completed bool
}

// DynamicRateLimiter implements a token-bucket rate limiter whose rate can
// be changed at runtime. A zero-value limiter is unlimited (no waiting).
// All methods are safe for concurrent use.
type DynamicRateLimiter struct {
	mu       sync.Mutex
	rate     float64 // bytes per second; 0 means unlimited
	tokens   float64
	lastTime time.Time
	queue    []*reservation
}

// refillLocked accrues budget for the time since the last refill, capped at
// one second of burst. Call with mu held.
func (d *DynamicRateLimiter) refillLocked(now time.Time) {
	if d.rate <= 0 {
		return
	}
	if d.lastTime.IsZero() {
		d.tokens = d.rate // start fully charged (1s burst)
		d.lastTime = now
		return
	}
	elapsed := now.Sub(d.lastTime).Seconds()
	if elapsed > 0 {
		d.tokens += elapsed * d.rate
		if d.tokens > d.rate {
			d.tokens = d.rate
		}
		d.lastTime = now
	}
}

// serveLocked assigns accumulated tokens to queued reservations head-first.
// Call with mu held.
func (d *DynamicRateLimiter) serveLocked() {
	for len(d.queue) > 0 && d.tokens > 0 {
		head := d.queue[0]
		if head.remaining <= d.tokens {
			d.tokens -= head.remaining
			head.remaining = 0
			head.completed = true
			d.queue = d.queue[1:]
		} else {
			head.remaining -= d.tokens
			d.tokens = 0
		}
	}
}

// drainLocked releases every queued waiter (used when the limit is removed).
// Call with mu held.
func (d *DynamicRateLimiter) drainLocked() {
	for _, r := range d.queue {
		r.completed = true
	}
	d.queue = nil
}

// removeLocked dequeues r without touching the token balance: tokens are
// only ever consumed by serveLocked toward actual progress, and nothing is
// consumed at enqueue time, so a cancelled waiter leaves no debt behind for
// others. Call with mu held.
func (d *DynamicRateLimiter) removeLocked(r *reservation) {
	for i, q := range d.queue {
		if q == r {
			d.queue = append(d.queue[:i], d.queue[i+1:]...)
			return
		}
	}
}

// SetRate changes the target rate in bytes per second. A value <= 0 means
// unlimited (queued waiters are released and Wait becomes a no-op).
func (d *DynamicRateLimiter) SetRate(bytesPerSec int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	newRate := float64(bytesPerSec)
	if newRate <= 0 {
		d.rate = 0
		d.tokens = 0
		d.lastTime = time.Time{}
		d.drainLocked()
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
	if !d.lastTime.IsZero() {
		d.lastTime = time.Now()
	}
	// A higher rate may make queued heads affordable right away.
	d.serveLocked()
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
// Reservations are queued FIFO and refills are assigned head-first, so each
// waiter matures on its own schedule: a later (even larger) reservation can
// never postpone an earlier reader, and cancelling a waiter simply drops its
// remaining need so no debt leaks to others. Waits proceed in short slices
// so a rate change, removal of the limit, or ctx cancellation is observed
// promptly even for multi-second waits.
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
	d.refillLocked(time.Now())
	if len(d.queue) == 0 && d.tokens >= need {
		d.tokens -= need
		d.mu.Unlock()
		return nil
	}
	// Enqueue the full need without consuming tokens: barging ahead of
	// queued readers would break FIFO fairness, and consuming nothing up
	// front means cancellation is a plain dequeue with no refund math.
	r := &reservation{remaining: need}
	d.queue = append(d.queue, r)
	d.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			d.mu.Lock()
			d.removeLocked(r)
			d.mu.Unlock()
			return ctx.Err()
		default:
		}

		d.mu.Lock()
		if d.rate <= 0 {
			// Limit removed while waiting: release everyone.
			d.drainLocked()
			d.mu.Unlock()
			return nil
		}
		d.refillLocked(time.Now())
		d.serveLocked()
		if r.completed {
			d.mu.Unlock()
			return nil
		}
		// Sleep only until the bytes ahead of (and including) this
		// reservation clear, or a short slice, whichever is smaller, so
		// cancellation and rate changes stay responsive.
		ahead := 0.0
		for _, q := range d.queue {
			ahead += q.remaining
			if q == r {
				break
			}
		}
		waitDur := time.Duration(ahead / d.rate * float64(time.Second))
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
			d.mu.Lock()
			d.removeLocked(r)
			d.mu.Unlock()
			return ctx.Err()
		}
	}
}

// Refund returns n unused bytes to the bucket (e.g. a read that was charged
// for a full chunk but transferred fewer bytes). The refund is capped at the
// one-second burst and immediately offered to queued heads.
func (d *DynamicRateLimiter) Refund(n int) {
	if n <= 0 || d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rate <= 0 {
		return
	}
	d.refillLocked(time.Now())
	d.tokens += float64(n)
	if d.tokens > d.rate {
		d.tokens = d.rate
	}
	d.serveLocked()
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
	k, err := r.inner.Read(p[:n])
	// Charge actual bytes, not the requested chunk: a short read, EOF, or
	// error after k < n bytes must refund the unused reservation, or one
	// byte trickling through a large buffer would spend the whole burst.
	if k < n && r.rl != nil {
		r.rl.Refund(n - k)
	}
	return k, err
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
