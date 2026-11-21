package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/core/services/remotes"
)

// remoteNeed is what the traffic of a remote over P2P needs from a relayed
// path. A public libp2p relay (128 KiB and 2 minutes per connection) does not
// fit, so model traffic never goes through one; a relay the user runs (1 GiB
// and 30 minutes by default) does.
var remoteNeed = p2pnet.Need{Bytes: 64 << 20, Duration: 10 * time.Minute}

// p2pTunnels runs the forwarders of the remotes over P2P. It keeps the
// wanted set across restarts of the P2P host: a forwarder runs while a host
// runs, and starts again on the same port when the host starts again.
//
// Distributed mode: the remotes service gets no Tunnels there, so this stays
// empty. Each forwarder is state of this instance only (its loopback port).
type p2pTunnels struct {
	n *p2pNet

	mu   sync.Mutex
	want map[string]remotes.Tunnel
	live map[string]*liveTunnel
	errs map[string]string
	host *p2pnet.Host
	ctx  context.Context
}

type liveTunnel struct {
	fwd *p2pnet.Forwarder

	mu sync.Mutex
	st remotes.P2PInfo
}

func (t *p2pTunnels) init() {
	if t.want == nil {
		t.want = map[string]remotes.Tunnel{}
		t.live = map[string]*liveTunnel{}
		t.errs = map[string]string{}
	}
}

// RemoteTunnels returns the P2P side of the remotes service.
func (a *Application) RemoteTunnels() remotes.Tunnels { return &a.p2p().tunnels }

// Open implements remotes.Tunnels.
func (t *p2pTunnels) Open(tn remotes.Tunnel) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	if tn.Port == 0 && t.host == nil {
		return 0, errP2POff()
	}
	t.closeLocked(tn.ID)
	t.want[tn.ID] = tn
	if t.host == nil {
		return tn.Port, nil
	}
	port, err := t.startLocked(tn)
	if err != nil {
		if tn.Port == 0 {
			delete(t.want, tn.ID)
		}
		return 0, err
	}
	return port, nil
}

// Close implements remotes.Tunnels.
func (t *p2pTunnels) Close(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	t.closeLocked(id)
	delete(t.want, id)
}

// Status implements remotes.Tunnels.
func (t *p2pTunnels) Status(id string) remotes.P2PInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	if t.host == nil {
		return remotes.P2PInfo{State: remotes.TunnelOff}
	}
	if lt := t.live[id]; lt != nil {
		lt.mu.Lock()
		defer lt.mu.Unlock()
		return lt.st
	}
	msg := t.errs[id]
	if msg == "" {
		msg = "the tunnel is not open"
	}
	return remotes.P2PInfo{State: remotes.TunnelError, Error: msg}
}

// startLocked starts the forwarder of tn and returns its port.
func (t *p2pTunnels) startLocked(tn remotes.Tunnel) (int, error) {
	delete(t.errs, tn.ID)
	fail := func(err error) (int, error) {
		t.errs[tn.ID] = err.Error()
		return 0, err
	}
	peerID, err := p2pnet.ParsePeerID(tn.Peer)
	if err != nil {
		return fail(err)
	}
	dial := []p2pnet.DialOption{p2pnet.WithNeed(remoteNeed)}
	if len(tn.Relays) > 0 {
		o, err := p2pnet.RelaysOption(tn.Relays)
		if err != nil {
			return fail(err)
		}
		dial = append(dial, o)
	}
	if len(tn.Addrs) > 0 {
		o, err := p2pnet.AddrsOption(tn.Addrs)
		if err != nil {
			return fail(err)
		}
		dial = append(dial, o)
	}
	lt := &liveTunnel{st: remotes.P2PInfo{State: remotes.TunnelListening}}
	fwd, err := t.host.Forward(t.ctx, peerID, p2pnet.ProtoHTTP, net.JoinHostPort("127.0.0.1", strconv.Itoa(tn.Port)),
		p2pnet.WithDialOptions(dial...),
		p2pnet.WithStatus(func(st p2pnet.ForwardStatus) { lt.update(st) }))
	if err != nil {
		if tn.Port != 0 {
			err = fmt.Errorf("port %d of this server is in use by another program: %w", tn.Port, err)
		}
		return fail(err)
	}
	lt.fwd = fwd
	t.live[tn.ID] = lt
	addr, ok := fwd.Addr().(*net.TCPAddr)
	if !ok {
		return tn.Port, nil
	}
	if tn.Port == 0 {
		tn.Port = addr.Port
		t.want[tn.ID] = tn
	}
	return addr.Port, nil
}

func (lt *liveTunnel) update(st p2pnet.ForwardStatus) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	switch st.State {
	case p2pnet.ForwardUp:
		lt.st = remotes.P2PInfo{State: remotes.TunnelUp, Path: string(st.Path.Kind), Limited: st.Path.Limited}
	case p2pnet.ForwardDown:
		info := remotes.P2PInfo{State: remotes.TunnelDown}
		if st.Err != nil {
			info.Error = st.Err.Error()
			if errors.Is(st.Err, p2pnet.ErrPathLimited) {
				info.Error = "the only path to the server is a relay whose limits are too small for model traffic; add a relay you run on both sides"
			}
		}
		lt.st = info
	case p2pnet.ForwardClosed:
		lt.st = remotes.P2PInfo{State: remotes.TunnelOff}
	}
}

func (t *p2pTunnels) closeLocked(id string) {
	if lt := t.live[id]; lt != nil {
		_ = lt.fwd.Close()
		delete(t.live, id)
	}
	delete(t.errs, id)
}

// start opens every wanted forwarder on a host that just started, then
// refreshes their remotes so their status is current.
func (t *p2pTunnels) start(rt *p2pRuntime) {
	t.mu.Lock()
	t.init()
	t.host, t.ctx = rt.host, rt.ctx
	ids := make([]string, 0, len(t.want))
	for id, tn := range t.want {
		if _, err := t.startLocked(tn); err != nil {
			xlog.Warn("P2P tunnel of a remote did not start", "remote", id, "error", err)
		}
		ids = append(ids, id)
	}
	t.mu.Unlock()
	svc := t.n.app.Remotes()
	if svc == nil || len(ids) == 0 {
		return
	}
	rt.wg.Go(func() {
		for _, id := range ids {
			if rt.ctx.Err() != nil {
				return
			}
			if _, err := svc.Refresh(rt.ctx, id); err != nil && !errors.Is(err, remotes.ErrNotFound) {
				xlog.Debug("Refresh of a remote over P2P failed", "remote", id, "error", err)
			}
		}
	})
}

// stop closes every forwarder; the wanted set stays for the next start.
func (t *p2pTunnels) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	for id := range t.live {
		t.closeLocked(id)
	}
	clear(t.errs)
	t.host, t.ctx = nil, nil
}

func errP2POff() error {
	return &remotes.InvalidError{Message: "P2P is off on this server. Set the P2P mode to Connect only (or Connect and share) to add a server by invite link."}
}

// Pair implements remotes.Tunnels: it redeems an invite of kind remote with
// this server's P2P identity.
func (t *p2pTunnels) Pair(ctx context.Context, link string, allowOpenRelay bool) (remotes.Pairing, error) {
	rt, mode := t.n.runtime()
	if rt == nil || mode == config.P2PModeOff {
		return remotes.Pairing{}, errP2POff()
	}
	l, err := p2ppair.ParseLink(link)
	if err != nil {
		return remotes.Pairing{}, &remotes.InvalidError{Message: "The invite link is not valid. Copy the whole link, starting with localai://pair."}
	}
	if l.Kind != p2ppair.KindRemote {
		return remotes.Pairing{}, &remotes.InvalidError{Message: fmt.Sprintf("This invite is for a %s, not for a remote. Ask for an invite to use the server as a remote.", l.Kind)}
	}
	name, _ := os.Hostname()
	if name == "" {
		name = "LocalAI"
	}
	res, err := pairclient.Redeem(ctx, rt.host, l, pairclient.Request{Name: name, Platform: runtime.GOOS, AllowOpenRelay: allowOpenRelay})
	if err != nil {
		return remotes.Pairing{}, pairError(err)
	}
	if res.ClientKey == nil || res.ClientKey.Secret == "" {
		return remotes.Pairing{}, &remotes.InvalidError{Message: "The server did not issue a key for this pairing. Ask for an invite to use the server as a remote."}
	}
	return remotes.Pairing{
		Peer: l.Peer.String(), ServerName: res.Server.Name, Phrase: res.Phrase, Key: res.ClientKey.Secret,
		Addrs: l.AddrStrings(), Relays: l.RelayAddrStrings(),
	}, nil
}

// pairError turns a redeem failure into a message for a person. The cause
// of an unreachable server goes to the log only.
func pairError(err error) error {
	msg := ""
	var reason error
	switch {
	case errors.Is(err, pairclient.ErrExpired):
		msg, reason = "The invite has expired. Ask for a new one.", remotes.ErrInviteGone
	case errors.Is(err, pairclient.ErrOpenRelay):
		msg = "The invite names an open relay, which anyone can use. The traffic stays encrypted, but the relay sees who talks to whom. Allow open relays to connect anyway."
	case errors.Is(err, pairclient.ErrGone):
		msg, reason = "The invite is not valid any more: it was used, cancelled or expired. Ask for a new one.", remotes.ErrInviteGone
	case errors.Is(err, pairclient.ErrTooManyTries):
		msg, reason = "The server saw too many wrong codes from this server. Wait a while and try again.", remotes.ErrInviteRateLimited
	case errors.Is(err, pairclient.ErrWrongServer):
		msg = "Another server answered than the one of the invite. Do not use this invite."
	case errors.Is(err, pairclient.ErrUnreachable):
		xlog.Warn("P2P pairing: the server of the invite is unreachable", "error", err)
		msg, reason = "The server of the invite cannot be reached. Check that it is online, or ask for an invite with a relay.", remotes.ErrInviteUnreachable
	default:
		xlog.Warn("P2P pairing failed", "error", err)
		msg = "The server refused the invite. Ask for a new one."
	}
	return &remotes.InvalidError{Message: msg, Reason: reason}
}
