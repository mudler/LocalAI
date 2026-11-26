package provider

import (
	"context"
	"time"
)

// SetSleep replaces how the transport waits for a loading model, so a test does
// not wait for real.
func SetSleep(t *Transport, f func(context.Context, time.Duration) error) {
	t.mu.Lock()
	t.sleep = f
	t.mu.Unlock()
}

// ParseRetryAfter is parseRetryAfter, for the tests.
var ParseRetryAfter = parseRetryAfter
