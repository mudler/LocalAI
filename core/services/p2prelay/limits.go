package p2prelay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// limitsFile is the name of the file in Config.DataDir that holds the limits
// an admin set with relayproto.OpLimits. A relay that finds it starts with
// these values instead of the flags, so a change made from a server stays
// over a restart. Delete the file to go back to the flags.
const limitsFile = "limits.json"

// maxLimitSeconds is the longest circuit time an admin may set: 100 years,
// the value that stands for "no limit" inside the circuit service.
const maxLimitSeconds = 100 * 365 * 24 * 3600

// storedLimits is the content of limits.json.
type storedLimits struct {
	CircuitMaxBytes   int64 `json:"circuit_max_bytes"`
	CircuitMaxSeconds int64 `json:"circuit_max_seconds"`
	RateBPS           int64 `json:"rate_bps"`
}

// applyStoredLimits replaces the per-circuit limits of cfg with the stored ones.
func applyStoredLimits(cfg *Config) error {
	path := filepath.Join(cfg.DataDir, limitsFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("p2prelay: read %s: %w", path, err)
	}
	var st storedLimits
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("p2prelay: parse %s: %w", path, err)
	}
	l, err := cfg.Limits.with(st.CircuitMaxBytes, st.CircuitMaxSeconds, st.RateBPS)
	if err != nil {
		return fmt.Errorf("p2prelay: %s: %w", path, err)
	}
	xlog.Info("relay: using the limits an admin set; delete the file to use the flags again", "file", path)
	cfg.Limits = l
	return nil
}

// with returns l with the per-circuit limits replaced, or an error when a
// value is not allowed.
func (l Limits) with(bytes, seconds, bps int64) (Limits, error) {
	if bytes < 0 || seconds < 0 || bps < 0 {
		return l, errors.New("limits must not be negative")
	}
	if seconds > maxLimitSeconds {
		return l, fmt.Errorf("circuit time must be at most %d seconds", maxLimitSeconds)
	}
	l.CircuitMaxBytes = bytes
	l.CircuitMaxDuration = time.Duration(seconds) * time.Second
	l.RateLimit = bps
	return l, nil
}

// reported maps the limits to the wire type.
func (l Limits) reported() relayproto.Limits {
	return relayproto.Limits{
		CircuitMaxBytes:   l.CircuitMaxBytes,
		CircuitMaxSeconds: int64(l.CircuitMaxDuration / time.Second),
		RateBPS:           l.RateLimit,
	}
}

// currentLimits returns the limits the relay runs with now.
func (r *Relay) currentLimits() Limits {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.Limits
}

// limitsOp serves relayproto.OpLimits for the admin p. Without a patch it
// reads. With one it saves the new limits and applies them: a change of the
// bandwidth limit reaches the next circuit at once. The circuit service of
// go-libp2p has no way to change the data and time limits of a running
// relay, so a change of those restarts it: the reservations are gone, circuits
// that run keep their old limits until they end, and the relay closes the
// connections of every peer but p so that they reserve again at once. p
// reserves again by itself. The returned peers are the ones to disconnect.
func (r *Relay) limitsOp(p peer.ID, patch *relayproto.LimitsPatch) (relayproto.Response, []peer.ID) {
	r.limitsMu.Lock()
	defer r.limitsMu.Unlock()

	r.mu.Lock()
	old := r.cfg.Limits
	r.mu.Unlock()
	next := old
	if patch != nil {
		bytes, seconds, bps := old.CircuitMaxBytes, int64(old.CircuitMaxDuration/time.Second), old.RateLimit
		if patch.CircuitMaxBytes != nil {
			bytes = *patch.CircuitMaxBytes
		}
		if patch.CircuitMaxSeconds != nil {
			seconds = *patch.CircuitMaxSeconds
		}
		if patch.RateBPS != nil {
			bps = *patch.RateBPS
		}
		var err error
		if next, err = old.with(bytes, seconds, bps); err != nil {
			return fail(err.Error()), nil
		}
	}
	resp := relayproto.Response{V: relayproto.Version, OK: true, Admin: true}
	rep := next.reported()
	if next == old {
		resp.Limits = &rep
		return resp, nil
	}
	if err := saveLimits(filepath.Join(r.cfg.DataDir, limitsFile), rep); err != nil {
		xlog.Error("relay: save limits", "error", err)
		return fail("could not save the limits"), nil
	}

	var drop []peer.ID
	r.mu.Lock()
	r.cfg.Limits = next
	r.rl.setPerCircuit(next.RateLimit)
	restart := next.CircuitMaxBytes != old.CircuitMaxBytes || next.CircuitMaxDuration != old.CircuitMaxDuration
	if restart {
		rel, err := r.restartCircuitServiceLocked()
		if err != nil {
			// The saved limits take effect at the next start of the relay.
			xlog.Error("relay: restart the circuit service", "error", err)
		} else {
			r.relay = rel
			drop = r.peersExceptLocked(p)
		}
	}
	r.mu.Unlock()
	rep.Restarted = restart
	resp.Limits = &rep
	xlog.Info("relay: limits changed by an admin", "admin", p, "circuit_max_bytes", rep.CircuitMaxBytes,
		"circuit_max_seconds", rep.CircuitMaxSeconds, "rate_bps", rep.RateBPS, "restarted", restart)
	return resp, drop
}

// restartCircuitServiceLocked replaces the circuit service with one that
// runs with the current limits. The caller holds mu.
func (r *Relay) restartCircuitServiceLocked() (*relayv2.Relay, error) {
	if err := r.relay.Close(); err != nil {
		return nil, err
	}
	return r.newCircuitRelayLocked()
}

// peersExceptLocked returns the connected peers but p. The caller holds mu.
func (r *Relay) peersExceptLocked(p peer.ID) []peer.ID {
	var out []peer.ID
	for _, id := range r.h.Network().Peers() {
		if id != p {
			out = append(out, id)
		}
	}
	return out
}

func saveLimits(path string, l relayproto.Limits) error {
	b, err := json.MarshalIndent(storedLimits{
		CircuitMaxBytes: l.CircuitMaxBytes, CircuitMaxSeconds: l.CircuitMaxSeconds, RateBPS: l.RateBPS,
	}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}
