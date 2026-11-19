package p2prelay

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// gate holds the membership state of a relay and makes every admission
// decision. It is the connection gater (layer 1), the circuit v2 ACL (layers 2
// and 3) and the state behind the admin and pass protocols (layer 4).
//
// Membership: a peer is admitted if it is an admin or a live member of some
// admin's set. A circuit is allowed only if one admin vouches for both ends,
// so the tenants of a shared relay cannot reach each other.
type gate struct {
	mu   sync.RWMutex
	path string
	st   state
	open bool

	admins map[peer.ID]bool
	// operators are the admins of the configuration (--admin). Only they may
	// change the limits that every tenant of a shared relay runs with.
	operators map[peer.ID]bool
	// members maps a member to the admins that vouch for it, with the time
	// its pending membership ends (zero: permanent).
	members map[peer.ID]map[peer.ID]time.Time
	// passes maps the hash of a pass code to its admin.
	passes map[string]peer.ID

	claimCode string

	maxPerSet   int
	maxPasses   int
	pendingTTL  time.Duration
	maxCircuits int64
	// circuits reports the open circuits, for the relay-wide cap.
	circuits func() int64
}

var errClaimRefused = errors.New("claim refused")

func newGate(cfg *Config) (*gate, error) {
	path := filepath.Join(cfg.DataDir, stateFile)
	st, err := loadState(path)
	if err != nil {
		return nil, err
	}
	g := &gate{
		path:        path,
		st:          st,
		open:        cfg.Open,
		admins:      map[peer.ID]bool{},
		operators:   map[peer.ID]bool{},
		maxPerSet:   cfg.MaxPeersPerAdmin,
		maxPasses:   cfg.MaxPassesPerAdmin,
		pendingTTL:  cfg.PendingTTL,
		maxCircuits: int64(cfg.MaxCircuits),
		circuits:    func() int64 { return 0 },
	}
	for _, p := range cfg.Admins {
		g.admins[p] = true
		g.operators[p] = true
	}
	for _, s := range st.ClaimedAdmins {
		p, err := peer.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("p2prelay: %s: claimed admin %q: %w", path, s, err)
		}
		g.admins[p] = true
	}
	// An admin removed from the configuration loses its set.
	for k := range g.st.Admins {
		p, err := peer.Decode(k)
		if err != nil || !g.admins[p] {
			xlog.Info("relay: dropping the set of a peer that is no longer an admin", "admin", k)
			delete(g.st.Admins, k)
		}
	}
	g.reindex()
	if g.unclaimedLocked() {
		g.claimCode, err = newClaimCode()
		if err != nil {
			return nil, err
		}
	}
	return g, nil
}

// newClaimCode returns 48 random bits as XXXX-XXXX-XXXX.
func newClaimCode() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("p2prelay: claim code: %w", err)
	}
	s := strings.ToUpper(hex.EncodeToString(b))
	return s[:4] + "-" + s[4:8] + "-" + s[8:], nil
}

func passHash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// reindex rebuilds the lookup maps from st. Callers hold mu for writing.
func (g *gate) reindex() {
	g.members = map[peer.ID]map[peer.ID]time.Time{}
	g.passes = map[string]peer.ID{}
	for k, set := range g.st.Admins {
		a, err := peer.Decode(k)
		if err != nil {
			continue
		}
		for mk, m := range set.Peers {
			mp, err := peer.Decode(mk)
			if err != nil {
				continue
			}
			if g.members[mp] == nil {
				g.members[mp] = map[peer.ID]time.Time{}
			}
			var until time.Time
			if m.PendingUntil != nil {
				until = *m.PendingUntil
			}
			g.members[mp][a] = until
		}
		for h := range set.Passes {
			g.passes[h] = a
		}
	}
}

func (g *gate) save() error {
	return saveState(g.path, &g.st)
}

func (g *gate) unclaimedLocked() bool { return !g.open && len(g.admins) == 0 }

func (g *gate) isAdmin(p peer.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.admins[p]
}

// canSetLimits tells whether p may change the limits of the relay: an admin of
// the configuration, or, when the configuration names none, the admin that
// claimed the relay, who is then its only operator.
func (g *gate) canSetLimits(p peer.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.operators) > 0 {
		return g.operators[p]
	}
	return g.admins[p]
}

// vouchers returns the admins that vouch for p at now, p itself if it is an
// admin. Callers hold mu.
func (g *gate) vouchers(p peer.ID, now time.Time) []peer.ID {
	var out []peer.ID
	if g.admins[p] {
		out = append(out, p)
	}
	for a, until := range g.members[p] {
		if until.IsZero() || now.Before(until) {
			out = append(out, a)
		}
	}
	return out
}

// admitted reports whether p may connect, reserve and open streams.
func (g *gate) admitted(p peer.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.open || len(g.vouchers(p, time.Now())) > 0
}

// passesPendingLocked reports whether some pass can still be used.
func (g *gate) passesPendingLocked(now time.Time) bool {
	for _, set := range g.st.Admins {
		for _, ps := range set.Passes {
			if ps.UsedBy == "" && now.Before(ps.Expires) {
				return true
			}
		}
	}
	return false
}

// strangerProtocol reports whether a peer that is not admitted may open an
// inbound stream of proto: the claim on an unclaimed relay, or a pass while
// one is pending. Nothing else.
func (g *gate) strangerProtocol(proto protocol.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	switch proto {
	case relayproto.AdminID:
		return g.unclaimedLocked()
	case relayproto.PassID:
		return g.passesPendingLocked(time.Now())
	}
	return false
}

// acceptsStrangers reports whether the gater lets peers that are not
// admitted finish the handshake.
func (g *gate) acceptsStrangers() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.unclaimedLocked() || g.passesPendingLocked(time.Now())
}

// AllowReserve implements the circuit v2 ACLFilter (layer 2).
func (g *gate) AllowReserve(p peer.ID, a ma.Multiaddr) bool {
	ok := g.admitted(p)
	if !ok {
		xlog.Debug("relay: deny reservation", "peer", p, "addr", a, "reason", "not paired")
	}
	return ok
}

// AllowConnect implements the circuit v2 ACLFilter (layer 3).
func (g *gate) AllowConnect(src peer.ID, _ ma.Multiaddr, dest peer.ID) bool {
	if g.maxCircuits > 0 && g.circuits() >= g.maxCircuits {
		xlog.Debug("relay: deny circuit", "src", src, "dest", dest, "reason", "relay full")
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.open {
		return true
	}
	now := time.Now()
	vd := g.vouchers(dest, now)
	for _, a := range g.vouchers(src, now) {
		if slices.Contains(vd, a) {
			xlog.Debug("relay: allow circuit", "src", src, "dest", dest, "admin", a)
			return true
		}
	}
	xlog.Debug("relay: deny circuit", "src", src, "dest", dest, "reason", "no common admin")
	return false
}

// InterceptPeerDial implements connmgr.ConnectionGater.
func (g *gate) InterceptPeerDial(peer.ID) bool { return true }

// InterceptAddrDial implements connmgr.ConnectionGater.
func (g *gate) InterceptAddrDial(peer.ID, ma.Multiaddr) bool { return true }

// InterceptAccept implements connmgr.ConnectionGater.
func (g *gate) InterceptAccept(network.ConnMultiaddrs) bool { return true }

// InterceptSecured implements connmgr.ConnectionGater (layer 1): an inbound
// peer that is not admitted is dropped right after the secure handshake,
// unless the relay is unclaimed or a pass is pending. Such a stranger may
// then open only the claim or the pass stream (see gatedResourceManager).
func (g *gate) InterceptSecured(d network.Direction, p peer.ID, cm network.ConnMultiaddrs) bool {
	if d == network.DirOutbound || g.admitted(p) || g.acceptsStrangers() {
		return true
	}
	xlog.Debug("relay: deny connection", "peer", p, "addr", cm.RemoteMultiaddr(), "reason", "not paired")
	return false
}

// InterceptUpgraded implements connmgr.ConnectionGater.
func (g *gate) InterceptUpgraded(network.Conn) (bool, control.DisconnectReason) { return true, 0 }

// claim makes p an admin if the relay is unclaimed and code is the claim
// code. The code works once.
func (g *gate) claim(p peer.ID, code string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	code = strings.ToUpper(strings.TrimSpace(code))
	if !g.unclaimedLocked() || g.claimCode == "" ||
		subtle.ConstantTimeCompare([]byte(code), []byte(g.claimCode)) != 1 {
		return errClaimRefused
	}
	g.admins[p] = true
	g.st.ClaimedAdmins = append(g.st.ClaimedAdmins, p.String())
	g.claimCode = ""
	if err := g.save(); err != nil {
		return err
	}
	xlog.Info("relay: claimed", "admin", p)
	return nil
}

// apply runs an admin operation of p, which must be an admin. It returns the
// response and the peers whose connections must be closed.
func (g *gate) apply(p peer.ID, req relayproto.Request, now time.Time) (relayproto.Response, []peer.ID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.admins[p] {
		return fail("not an admin"), nil
	}
	key := p.String()
	// The operation changes a copy, which replaces the set only after the
	// state is saved, so a failed save leaves the memory as the disk is.
	orig := g.st.Admins[key]
	set := orig.clone()
	var drop []peer.ID
	changed := false
	switch req.Op {
	case relayproto.OpStatus, relayproto.OpList:
	case relayproto.OpAdd, relayproto.OpRemove, relayproto.OpReplace:
		if req.Seq <= 0 {
			return fail("seq required"), nil
		}
		if req.Seq < set.Seq {
			r := fail(fmt.Sprintf("stale seq %d < %d", req.Seq, set.Seq))
			r.Seq = set.Seq
			return r, nil
		}
		ids, err := decodePeers(req.Peers)
		if err != nil {
			return fail(err.Error()), nil
		}
		next := maps.Clone(set.Peers)
		var removed []peer.ID
		switch req.Op {
		case relayproto.OpAdd:
			for i, id := range ids {
				next[id.String()] = confirm(next[id.String()], req.Peers[i].Label, now)
			}
		case relayproto.OpRemove:
			for _, id := range ids {
				if _, ok := next[id.String()]; ok {
					delete(next, id.String())
					removed = append(removed, id)
				}
			}
		case relayproto.OpReplace:
			want := map[string]bool{}
			for i, id := range ids {
				want[id.String()] = true
				next[id.String()] = confirm(next[id.String()], req.Peers[i].Label, now)
			}
			// Pending members stay: the admin confirms them after the redeem,
			// and a periodic replace must not undo a pass in flight.
			for k, m := range next {
				if !want[k] && !m.pending() {
					delete(next, k)
					if id, err := peer.Decode(k); err == nil {
						removed = append(removed, id)
					}
				}
			}
		}
		if len(next) > g.maxPerSet {
			r := fail(fmt.Sprintf("too many peers: %d > %d", len(next), g.maxPerSet))
			r.MaxPeers = g.maxPerSet
			return r, nil
		}
		set.Peers, set.Seq = next, req.Seq
		changed = true
		for _, id := range removed {
			if !g.admins[id] {
				drop = append(drop, id)
			}
		}
	case relayproto.OpPass:
		if err := g.addPass(p, set, req.Pass, now); err != nil {
			return fail(err.Error()), nil
		}
		changed = true
	case relayproto.OpPassCancel:
		if req.Pass == nil || req.Pass.Code == "" {
			return fail("pass code required"), nil
		}
		h := passHash(req.Pass.Code)
		// A code of another admin is left alone and answered the same way.
		if ps, ok := set.Passes[h]; ok {
			delete(set.Passes, h)
			if m, ok := set.Peers[ps.UsedBy]; ok && m.pending() {
				delete(set.Peers, ps.UsedBy)
				if id, err := peer.Decode(ps.UsedBy); err == nil && !g.admins[id] {
					drop = append(drop, id)
				}
			}
			changed = true
		}
	default:
		return fail("unknown op " + req.Op), nil
	}
	if changed {
		set.Updated = now
		g.st.Admins[key] = set
		g.reindex()
		if err := g.save(); err != nil {
			xlog.Error("relay: cannot save the allowlist", "error", err)
			if orig == nil {
				delete(g.st.Admins, key)
			} else {
				g.st.Admins[key] = orig
			}
			g.reindex()
			return fail("state not saved: " + err.Error()), nil
		}
		xlog.Info("relay: admin operation", "admin", p, "op", req.Op, "members", len(set.Peers), "seq", set.Seq, "revoked", len(drop))
	}
	resp := relayproto.Response{V: relayproto.Version, OK: true, Admin: true, Seq: set.Seq, MaxPeers: g.maxPerSet}
	var ids []peer.ID
	for k, m := range set.Peers {
		id, err := peer.Decode(k)
		if err != nil {
			continue
		}
		if !m.pending() {
			ids = append(ids, id)
		}
		if req.Op == relayproto.OpList {
			resp.Peers = append(resp.Peers, relayproto.Peer{ID: k, Label: m.Label, Pending: m.pending()})
		}
	}
	slices.SortFunc(resp.Peers, func(a, b relayproto.Peer) int { return strings.Compare(a.ID, b.ID) })
	resp.Digest, resp.Count = relayproto.Digest(ids), len(ids)
	return resp, drop
}

// confirm makes a member permanent with an optional new label.
func confirm(m member, label string, now time.Time) member {
	if m.Added.IsZero() {
		m.Added = now
	}
	m.PendingUntil = nil
	if label != "" {
		m.Label = label
	}
	return m
}

func (g *gate) addPass(admin peer.ID, set *adminSet, p *relayproto.Pass, now time.Time) error {
	if p == nil || len(p.Code) < 8 || len(p.Code) > 256 {
		return errors.New("pass code must have 8 to 256 characters")
	}
	ttl := time.Duration(p.TTLSeconds) * time.Second
	if ttl <= 0 || ttl > MaxPassTTL {
		return fmt.Errorf("pass ttl must be between 1 s and %v", MaxPassTTL)
	}
	h := passHash(p.Code)
	if owner, ok := g.passes[h]; ok && owner != admin {
		return errors.New("pass code in use")
	}
	if _, ok := set.Passes[h]; !ok {
		open := 0
		for _, ps := range set.Passes {
			if ps.UsedBy == "" && now.Before(ps.Expires) {
				open++
			}
		}
		if open >= g.maxPasses {
			return fmt.Errorf("too many open passes: %d", open)
		}
	} else if set.Passes[h].UsedBy != "" {
		return errors.New("pass already used")
	}
	exp := now.Add(ttl)
	set.Passes[h] = pass{Label: p.Label, Expires: exp, Keep: exp}
	return nil
}

// usePass admits p as a pending member of the admin that registered code,
// if the pass exists, has not expired and was never used. It returns the
// admin and how long the pending membership lasts.
func (g *gate) usePass(p peer.ID, code string, now time.Time) (peer.ID, time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := passHash(code)
	a, ok := g.passes[h]
	if !ok || !g.admins[a] {
		return "", 0, false
	}
	set := g.st.Admins[a.String()]
	ps, ok := set.Passes[h]
	if !ok || ps.UsedBy != "" || !now.Before(ps.Expires) {
		return "", 0, false
	}
	m, exists := set.Peers[p.String()]
	if !exists && len(set.Peers) >= g.maxPerSet {
		return "", 0, false
	}
	until := now.Add(g.pendingTTL)
	ps.UsedBy = p.String()
	ps.Keep = until
	set.Passes[h] = ps
	if !exists || m.pending() {
		m.Added = now
		m.Label = ps.Label
		m.PendingUntil = &until
		set.Peers[p.String()] = m
	}
	set.Updated = now
	g.reindex()
	if err := g.save(); err != nil {
		xlog.Error("relay: cannot save the allowlist", "error", err)
	}
	return a, g.pendingTTL, true
}

// sweep drops expired pending members and old passes. It returns the peers
// that lost their last admission and must be disconnected.
func (g *gate) sweep(now time.Time) []peer.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	var expired []peer.ID
	changed := false
	for _, set := range g.st.Admins {
		for k, m := range set.Peers {
			if !m.live(now) {
				delete(set.Peers, k)
				changed = true
				if id, err := peer.Decode(k); err == nil {
					expired = append(expired, id)
				}
			}
		}
		for h, ps := range set.Passes {
			if !now.Before(ps.Keep) && !now.Before(ps.Expires) {
				delete(set.Passes, h)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	g.reindex()
	if err := g.save(); err != nil {
		xlog.Error("relay: cannot save the allowlist", "error", err)
	}
	var drop []peer.ID
	for _, id := range expired {
		if !g.open && len(g.vouchers(id, now)) == 0 {
			drop = append(drop, id)
		}
	}
	return drop
}

// counts returns the numbers for Status. Callers hold no lock.
func (g *gate) counts(now time.Time) (admins, members, pending, passes int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, vs := range g.members {
		perm, pend := false, false
		for _, until := range vs {
			switch {
			case until.IsZero():
				perm = true
			case now.Before(until):
				pend = true
			}
		}
		if perm {
			members++
		} else if pend {
			pending++
		}
	}
	for _, set := range g.st.Admins {
		for _, ps := range set.Passes {
			if ps.UsedBy == "" && now.Before(ps.Expires) {
				passes++
			}
		}
	}
	return len(g.admins), members, pending, passes
}

func (g *gate) mode() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	switch {
	case g.open:
		return relayproto.ModeOpen
	case g.unclaimedLocked():
		return relayproto.ModeUnclaimed
	}
	return relayproto.ModeGated
}

func decodePeers(ps []relayproto.Peer) ([]peer.ID, error) {
	ids := make([]peer.ID, 0, len(ps))
	for _, p := range ps {
		id, err := peer.Decode(p.ID)
		if err != nil {
			return nil, fmt.Errorf("bad peer ID %q", p.ID)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func fail(msg string) relayproto.Response {
	return relayproto.Response{V: relayproto.Version, Err: msg}
}
