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

// limitsRestartCooldown is the shortest time between two changes that restart
// the circuit service. A restart drops the reservations and the connections of
// every member, so an admin must not be able to repeat it in a loop.
const limitsRestartCooldown = 30 * time.Second

// maxLimitSeconds is the longest circuit time an admin may set: 100 years,
// the value that stands for "no limit" inside the circuit service.
const maxLimitSeconds = 100 * 365 * 24 * 3600

// storedLimits is the content of limits.json.
type storedLimits struct {
	CircuitMaxBytes   int64 `json:"circuit_max_bytes"`
	CircuitMaxSeconds int64 `json:"circuit_max_seconds"`
	RateBPS           int64 `json:"rate_bps"`
}

// ceilingValue is a limit and its ceiling in the units of the wire type.
type ceilingValue struct {
	name    string
	value   int64
	ceiling int64
	format  func(int64) string
}

// over reports whether value is above the ceiling. Zero means "no limit" for
// the value and "no ceiling" for the ceiling, so with a ceiling a value of
// zero is above it.
func (c ceilingValue) over() bool {
	return c.ceiling > 0 && (c.value == 0 || c.value > c.ceiling)
}

// clamped is the value when it is inside the ceiling, else the ceiling.
func (c ceilingValue) clamped() int64 {
	if c.over() {
		return c.ceiling
	}
	return c.value
}

// err describes why the value is above the ceiling.
func (c ceilingValue) err() error {
	if c.value == 0 {
		return fmt.Errorf("the %s cannot be removed: the relay operator set a ceiling of %s", c.name, c.format(c.ceiling))
	}
	return fmt.Errorf("the %s of %s is above the ceiling of %s set by the relay operator", c.name, c.format(c.value), c.format(c.ceiling))
}

func formatSeconds(n int64) string { return (time.Duration(n) * time.Second).String() }

func formatRate(n int64) string { return formatBytes(n) + "/s" }

// values pairs the limits with the ceiling, in a fixed order.
func (c Ceiling) values(bytes, seconds, bps int64) []ceilingValue {
	return []ceilingValue{
		{"circuit data limit", bytes, c.CircuitMaxBytes, formatBytes},
		{"circuit time limit", seconds, int64(c.CircuitMaxDuration / time.Second), formatSeconds},
		{"circuit bandwidth limit", bps, c.RateLimit, formatRate},
	}
}

func (c Ceiling) validate() error {
	if c.CircuitMaxBytes < 0 || c.CircuitMaxDuration < 0 || c.RateLimit < 0 {
		return errors.New("p2prelay: the ceiling must not be negative")
	}
	return nil
}

// reported maps the ceiling to the wire type.
func (c Ceiling) reported() relayproto.LimitsCeiling {
	return relayproto.LimitsCeiling{
		CircuitMaxBytes:   c.CircuitMaxBytes,
		CircuitMaxSeconds: int64(c.CircuitMaxDuration / time.Second),
		RateBPS:           c.RateLimit,
	}
}

// check returns an error that names the ceiling when one of the limits is
// above it.
func (c Ceiling) check(l Limits) error {
	for _, v := range c.values(l.CircuitMaxBytes, int64(l.CircuitMaxDuration/time.Second), l.RateLimit) {
		if v.over() {
			return v.err()
		}
	}
	return nil
}

// checkStart is check for the limits of the flags: it refuses to start a relay
// whose limits are outside its own ceiling.
func (c Ceiling) checkStart(l Limits) error {
	if err := c.check(l); err != nil {
		return fmt.Errorf("p2prelay: %w (lower the limit or raise the ceiling)", err)
	}
	return nil
}

// clamp returns l with every limit that is above the ceiling replaced by the
// ceiling, and the names of the limits it changed.
func (c Ceiling) clamp(l Limits) (Limits, []string) {
	vals := c.values(l.CircuitMaxBytes, int64(l.CircuitMaxDuration/time.Second), l.RateLimit)
	var changed []string
	for i, v := range vals {
		if v.over() {
			changed = append(changed, v.name)
			vals[i].value = v.clamped()
		}
	}
	l.CircuitMaxBytes = vals[0].value
	l.CircuitMaxDuration = time.Duration(vals[1].value) * time.Second
	l.RateLimit = vals[2].value
	return l, changed
}

// applyStoredLimits replaces the per-circuit limits of cfg with the stored ones.
func applyStoredLimits(cfg *Config) error {
	path := filepath.Join(cfg.DataDir, limitsFile)
	b, err := os.ReadFile(path) // #nosec G304 -- the file is under the relay data directory and its name is a constant
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
	if clamped, names := cfg.Ceiling.clamp(l); len(names) > 0 {
		xlog.Warn("relay: the saved limits are above the ceiling of this relay: using the ceiling for them",
			"file", path, "limits", names)
		l = clamped
	}
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
	if patch != nil && !r.gate.canSetLimits(p) {
		xlog.Debug("relay: deny limits change", "peer", p, "reason", "not an operator")
		return fail("only an operator of the relay can change the limits"), nil
	}
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
		if err := r.cfg.Ceiling.check(next); err != nil {
			return fail(err.Error()), nil
		}
	}
	resp := relayproto.Response{V: relayproto.Version, OK: true, Admin: true}
	rep := next.reported()
	rep.Ceiling = r.cfg.Ceiling.reported()
	if next == old {
		resp.Limits = &rep
		return resp, nil
	}
	restart := next.CircuitMaxBytes != old.CircuitMaxBytes || next.CircuitMaxDuration != old.CircuitMaxDuration
	if restart {
		if wait := limitsRestartCooldown - time.Since(r.lastRestart); !r.lastRestart.IsZero() && wait > 0 {
			return fail(fmt.Sprintf("limits changed too recently, retry in %d seconds", int(wait/time.Second)+1)), nil
		}
	}
	path := filepath.Join(r.cfg.DataDir, limitsFile)
	prev, hadFile := readLimitsFile(path)
	if err := saveLimits(path, rep); err != nil {
		xlog.Error("relay: save limits", "error", err)
		return fail("could not save the limits"), nil
	}

	var drop []peer.ID
	r.mu.Lock()
	r.cfg.Limits = next
	r.rl.setPerCircuit(next.RateLimit)
	if restart {
		rel, err := r.restartCircuitServiceLocked(old)
		if err != nil {
			// Nothing of the change applies: the saved limits and the
			// running ones go back to the old values.
			xlog.Error("relay: restart the circuit service", "error", err)
			r.cfg.Limits = old
			r.rl.setPerCircuit(old.RateLimit)
			r.mu.Unlock()
			restoreLimitsFile(path, prev, hadFile)
			return fail("could not apply the limits"), nil
		}
		r.relay = rel
		r.lastRestart = time.Now()
		drop = r.peersExceptLocked(p)
	}
	r.mu.Unlock()
	rep.Restarted = restart
	resp.Limits = &rep
	xlog.Info("relay: limits changed by an admin", "admin", p, "circuit_max_bytes", rep.CircuitMaxBytes,
		"circuit_max_seconds", rep.CircuitMaxSeconds, "rate_bps", rep.RateBPS, "restarted", restart)
	return resp, drop
}

// restartCircuitServiceLocked replaces the circuit service with one that
// runs with the current limits. The new service cannot start before the old
// one is closed, because both register the same stream handlers on the host
// and closing the old one removes them. When the new service does not start,
// it starts again with the limits in old, so the relay is never left with a
// closed service. The caller holds mu and sets r.cfg.Limits back to old on an
// error.
func (r *Relay) restartCircuitServiceLocked(old Limits) (*relayv2.Relay, error) {
	if r.relay != nil {
		if err := r.relay.Close(); err != nil {
			xlog.Warn("relay: close the circuit service", "error", err)
		}
	}
	rel, err := r.newCircuitRelayLocked()
	if err == nil {
		return rel, nil
	}
	next := r.cfg.Limits
	r.cfg.Limits = old
	rel, rerr := r.newCircuitRelayLocked()
	r.cfg.Limits = next
	if rerr != nil {
		// No service runs now. Close skips the closed one.
		r.relay = nil
		return nil, fmt.Errorf("%w; the old limits did not start either: %v", err, rerr)
	}
	r.relay = rel
	return nil, err
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

// readLimitsFile returns the content of the saved limits and whether the file exists.
func readLimitsFile(path string) ([]byte, bool) {
	b, err := os.ReadFile(path) // #nosec G304 -- path comes from the relay's own data directory
	return b, err == nil
}

// restoreLimitsFile puts the saved limits back as they were before a change
// that did not apply.
func restoreLimitsFile(path string, prev []byte, had bool) {
	var err error
	if had {
		err = writeFileAtomic(path, prev)
	} else if err = os.Remove(path); errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		xlog.Error("relay: restore the saved limits", "error", err)
	}
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
