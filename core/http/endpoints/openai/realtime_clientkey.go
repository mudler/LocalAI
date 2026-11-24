// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"time"

	"github.com/mudler/xlog"
)

// clientKeyRecheckInterval is how often a realtime session of a paired client
// asks whether its key still stands. A session lives for minutes or hours, and
// the key is only read at the handshake, so without this a revoked client would
// keep talking until it chose to hang up. It is a variable for the tests.
var clientKeyRecheckInterval = 5 * time.Second

// clientKeyMaxStoreErrors is the number of failed checks in a row after which
// the session closes anyway: one failed query is a blip, a store that stays down
// is a reason to stop trusting a key that nobody can verify.
const clientKeyMaxStoreErrors = 3

// watchClientKey asks status every interval and closes the transport when the
// key is revoked, expired or paused, its owner is no longer allowed to have it,
// or the model of the session left its list. It returns a function that ends the
// watch; call it when the session ends. The check is by time and not by message:
// an audio stream sends a message every few tens of milliseconds, and one query
// per message would cost more than it saves. A check that does not answer within
// one interval counts as a failed check, so a store that hangs triggers the
// failure rule like a store that errors. The close ends the read of the session,
// which then cleans up as for any hangup.
func watchClientKey(t Transport, status func(ctx context.Context) (bool, error), interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		tick := time.NewTicker(interval)
		defer tick.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			active, err := boundedStatus(ctx, status, interval)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				failures++
				xlog.Warn("realtime: could not check the key of a paired client", "error", err, "failures", failures)
				if failures < clientKeyMaxStoreErrors {
					continue
				}
			} else {
				failures = 0
				if active {
					continue
				}
			}
			xlog.Info("realtime: closing the session of a paired client whose key is no longer valid")
			sendError(t, "client_key_invalid", "this key was revoked, has expired or is paused, or may no longer use this model", "", "")
			_ = t.Close()
			return
		}
	}()
	return cancel
}

// boundedStatus runs one check with a timeout. The check gets the context, and
// the wait ends with it even when the check ignores it.
func boundedStatus(ctx context.Context, status func(ctx context.Context) (bool, error), timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type answer struct {
		active bool
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		active, err := status(ctx)
		done <- answer{active, err}
	}()
	select {
	case a := <-done:
		return a.active, a.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
