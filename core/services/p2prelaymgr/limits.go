package p2prelaymgr

import (
	"context"
	"errors"
	"fmt"

	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// Limits implements Manager.
func (m *manager) Limits(ctx context.Context, id string) (LimitsInfo, error) {
	e, err := m.find(id)
	if err != nil {
		return LimitsInfo{}, err
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	resp, err := m.status(ctx, e)
	if err != nil {
		if m.refused(ctx, e, resp, err) {
			// A gated relay that does not list this server closes the
			// connection: show what it reported when it last answered.
			return LimitsInfo{Limits: e.snapshot().Limits}, nil
		}
		return LimitsInfo{}, err
	}
	if resp.Status == nil {
		return LimitsInfo{}, fmt.Errorf("the relay sent no status")
	}
	l := limitsOf(resp.Status)
	e.mu.Lock()
	e.relay.Limits = l
	e.mu.Unlock()
	return LimitsInfo{
		Limits: l, Ceiling: ceilingOf(resp.Status.Ceiling),
		CanChange: resp.Admin && resp.Status.Mode == relayproto.ModeGated,
	}, nil
}

// SetLimits implements Manager.
func (m *manager) SetLimits(ctx context.Context, id string, patch LimitsPatch) (LimitsInfo, error) {
	e, err := m.find(id)
	if err != nil {
		return LimitsInfo{}, err
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	st, err := m.status(ctx, e)
	if err != nil {
		if m.refused(ctx, e, st, err) {
			return LimitsInfo{}, ErrNotAdmin
		}
		return LimitsInfo{}, err
	}
	if !st.Admin || st.Status == nil || st.Status.Mode != relayproto.ModeGated {
		return LimitsInfo{}, ErrNotAdmin
	}
	resp, err := relayproto.Call(ctx, m.lh(), e.info.ID, relayproto.Request{
		Op: relayproto.OpLimits,
		Limits: &relayproto.LimitsPatch{
			CircuitMaxBytes: patch.CircuitBytes, CircuitMaxSeconds: patch.CircuitSeconds, RateBPS: patch.RateBPS,
		},
	})
	if err != nil {
		if resp.V != 0 && !resp.OK {
			return LimitsInfo{}, fmt.Errorf("%w: %s", ErrInvalidLimits, resp.Err)
		}
		return LimitsInfo{}, err
	}
	if resp.Limits == nil {
		return LimitsInfo{}, fmt.Errorf("the relay sent no limits")
	}
	l := Limits{
		CircuitBytes: resp.Limits.CircuitMaxBytes, CircuitSeconds: resp.Limits.CircuitMaxSeconds, RateBPS: resp.Limits.RateBPS,
	}
	e.mu.Lock()
	e.relay.Limits = l
	e.mu.Unlock()
	if resp.Limits.Restarted {
		// The relay dropped its reservations. Ours comes back at once; the
		// other peers' connections were closed, so they reserve again.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), connectTimeout)
		defer cancel()
		if _, err := client.Reserve(rctx, m.lh(), e.info); err != nil {
			xlog.Debug("reserve again after a limits change failed", "relay", id, "error", err)
		}
	}
	return LimitsInfo{Limits: l, Ceiling: ceilingOf(resp.Limits.Ceiling), CanChange: true, Restarted: resp.Limits.Restarted}, nil
}

func limitsOf(st *relayproto.Status) Limits {
	return Limits{CircuitBytes: st.CircuitMaxBytes, CircuitSeconds: st.CircuitMaxSeconds, RateBPS: st.RateBPS}
}

func ceilingOf(c relayproto.LimitsCeiling) Limits {
	return Limits{CircuitBytes: c.CircuitMaxBytes, CircuitSeconds: c.CircuitMaxSeconds, RateBPS: c.RateBPS}
}

// refused reports whether a failed status call means that the relay's gate
// refused this server, as opposed to a relay that does not answer.
func (m *manager) refused(ctx context.Context, e *entry, resp relayproto.Response, err error) bool {
	if ctx.Err() != nil || resp.V != 0 || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return m.connect(ctx, e) == nil
}
