package p2prelay

import (
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
	"golang.org/x/time/rate"

	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// errPassRefused is the one answer to every failed pass: unknown, used,
// expired, malformed or over the rate cap.
const errPassRefused = "pass refused"

func (r *Relay) handleAdmin(s network.Stream) {
	defer func() { _ = s.Close() }()
	p := s.Conn().RemotePeer()
	if !r.enter(p) {
		r.reply(s, fail("busy"))
		return
	}
	defer r.leave(p)
	_ = s.SetDeadline(time.Now().Add(adminStreamTimeout))
	var req relayproto.Request
	if err := relayproto.Read(s, &req); err != nil {
		r.reply(s, fail("bad request"))
		return
	}
	if req.V != relayproto.Version {
		r.reply(s, fail("unsupported version"))
		return
	}
	resp, drop := r.adminOp(p, s.Conn().RemoteMultiaddr(), req)
	r.reply(s, resp)
	r.disconnect(drop, "removed by admin")
}

func (r *Relay) adminOp(p peer.ID, from ma.Multiaddr, req relayproto.Request) (relayproto.Response, []peer.ID) {
	now := time.Now()
	if req.Op == relayproto.OpClaim {
		if !r.claimTries.allow(ipKey(from), now) {
			return fail(errClaimRefused.Error()), nil
		}
		if err := r.gate.claim(p, req.Code); err != nil {
			xlog.Info("relay: claim refused", "peer", p, "addr", from)
			return fail(errClaimRefused.Error()), nil
		}
		return relayproto.Response{V: relayproto.Version, OK: true, Admin: true}, nil
	}
	if !r.gate.isAdmin(p) {
		if req.Op == relayproto.OpStatus {
			r.mu.Lock()
			st := r.statusLocked(from)
			r.mu.Unlock()
			return relayproto.Response{V: relayproto.Version, OK: true, Status: publicStatus(st)}, nil
		}
		xlog.Debug("relay: deny admin operation", "peer", p, "op", req.Op, "reason", "not an admin")
		return fail("not an admin"), nil
	}
	switch req.Op {
	case relayproto.OpAdd, relayproto.OpRemove, relayproto.OpReplace, relayproto.OpPass, relayproto.OpPassCancel:
		if !r.allowWrite(p, now) {
			return fail("rate limited"), nil
		}
	case relayproto.OpLimits:
		if req.Limits != nil && !r.allowWrite(p, now) {
			return fail("rate limited"), nil
		}
		return r.limitsOp(p, req.Limits)
	}
	resp, drop := r.gate.apply(p, req, now)
	if resp.OK && req.Op == relayproto.OpStatus {
		r.mu.Lock()
		st := r.statusLocked(from)
		r.mu.Unlock()
		resp.Status = &st
	}
	return resp, drop
}

// enter admits one more admin stream of p, at most adminStreamsPerPeer at once.
func (r *Relay) enter(p peer.ID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy[p] >= adminStreamsPerPeer {
		return false
	}
	r.busy[p]++
	return true
}

func (r *Relay) leave(p peer.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy[p]--; r.busy[p] <= 0 {
		delete(r.busy, p)
	}
}

// allowWrite caps the changes of one admin, because every change rewrites
// the state file.
func (r *Relay) allowWrite(p peer.ID, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.writes[p]
	if l == nil {
		l = rate.NewLimiter(adminWritesPerSec, adminWritesPerSec)
		r.writes[p] = l
	}
	return l.AllowN(now, 1)
}

func (r *Relay) reply(s network.Stream, v any) {
	if err := relayproto.Write(s, v); err != nil {
		xlog.Debug("relay: write response", "peer", s.Conn().RemotePeer(), "error", err)
		_ = s.Reset()
	}
}

// handlePass serves relayproto.PassID. A peer gets one try per connection,
// and the tries of one peer and of one IP are capped, so codes cannot be
// guessed. The answer to a failed try never says why.
func (r *Relay) handlePass(s network.Stream) {
	defer func() { _ = s.Close() }()
	_ = s.SetDeadline(time.Now().Add(r.cfg.StrangerTimeout))
	p := s.Conn().RemotePeer()
	refused := relayproto.PassResponse{V: relayproto.Version, Err: errPassRefused}

	r.mu.Lock()
	first := !r.tried[p]
	r.tried[p] = true
	r.mu.Unlock()

	var req relayproto.PassRequest
	if err := relayproto.Read(s, &req); err != nil || req.V != relayproto.Version || !first {
		r.reply(s, refused)
		return
	}
	now := time.Now()
	ipOK := r.passIP.allow(ipKey(s.Conn().RemoteMultiaddr()), now)
	peerOK := r.passPeer.allow(p.String(), now)
	if !ipOK || !peerOK {
		xlog.Info("relay: pass refused", "peer", p, "addr", s.Conn().RemoteMultiaddr(), "reason", "too many tries")
		r.reply(s, refused)
		return
	}
	admin, ttl, ok := r.gate.usePass(p, req.Code, now)
	if !ok {
		xlog.Info("relay: pass refused", "peer", p, "addr", s.Conn().RemoteMultiaddr())
		r.reply(s, refused)
		return
	}
	xlog.Info("relay: pass used", "peer", p, "admin", admin, "pending_seconds", int(ttl/time.Second))
	r.reply(s, relayproto.PassResponse{V: relayproto.Version, OK: true, TTLSeconds: int(ttl / time.Second)})
}
