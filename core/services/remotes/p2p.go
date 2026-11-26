package remotes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mudler/xlog"
)

// Remotes over P2P. The remote is another LocalAI that issued an invite of
// kind remote. Redeeming it admits this server's peer at the other server and
// returns an API key for it. This server then runs a forwarder: a loopback
// port whose connections go over P2P to the peer. Everything else treats the
// remote as a LocalAI at http://127.0.0.1:<port>: the listings, the entries
// (proxy.upstream_url) and the localai-proxy backend, which never learns
// about P2P. The port is chosen once and kept in the record, so the entries
// stay valid across restarts.

// defaultP2PName is the name of a remote added by invite when neither the
// request nor the server gives one.
const defaultP2PName = "LocalAI over P2P"

func loopbackURL(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

// recordTarget returns the target of a stored remote: its address, or the
// loopback address of the forwarder for a remote over P2P.
func recordTarget(r record, key string) (target, error) {
	if r.Transport != TransportP2P {
		return parseTarget(r.Kind, r.URL, key)
	}
	if r.P2P == nil || r.P2P.Port <= 0 {
		return target{}, upstreamErr(StepInvalidURL, "The remote over P2P has no local port. Remove it and add it again.")
	}
	return parseTarget(r.Kind, loopbackURL(r.P2P.Port), key)
}

// tunnelReady says whether the forwarder of a remote over P2P holds its
// port. It is nil for any other remote.
func (s *service) tunnelReady(r record) error {
	if r.Transport != TransportP2P {
		return nil
	}
	if s.o.Tunnels == nil {
		return upstreamErr(StepUnreachable, "Remotes over P2P are not available on this server.")
	}
	switch st := s.o.Tunnels.Status(r.ID); st.State {
	case TunnelOff:
		return upstreamErr(StepUnreachable, "P2P is off on this server. Set the P2P mode to Connect only or Connect and share.")
	case TunnelError:
		return upstreamErr(StepUnreachable, "The P2P tunnel is not running: %s", st.Error)
	}
	return nil
}

// viewOf is the public view of a record with the live state of its tunnel.
func (s *service) viewOf(r record) Remote {
	v := r.view()
	if r.Transport != TransportP2P || r.P2P == nil {
		return v
	}
	info := P2PInfo{PeerID: r.P2P.Peer, LocalPort: r.P2P.Port, State: TunnelOff}
	if s.o.Tunnels != nil {
		info = s.o.Tunnels.Status(r.ID)
		info.PeerID, info.LocalPort = r.P2P.Peer, r.P2P.Port
	}
	v.P2P = &info
	return v
}

// createP2P adds a remote from an invite link of kind remote.
func (s *service) createP2P(ctx context.Context, r CreateRequest) (Result, error) {
	if s.o.Tunnels == nil {
		return Result{}, invalid("Remotes over P2P are not available on this server. In distributed mode a model may load on a worker, which cannot use this server's P2P connection.")
	}
	if r.Kind != "" && r.Kind != KindLocalAI {
		return Result{}, invalid("An invite link adds a LocalAI server: the kind must be localai.")
	}
	if strings.TrimSpace(r.URL) != "" || strings.TrimSpace(r.APIKey) != "" {
		return Result{}, invalid("Send either an invite link or an address and a key, not both.")
	}
	if len(r.Models) > MaxAdoptModels {
		return Result{}, tooManyModels()
	}
	var name string
	if strings.TrimSpace(r.Name) != "" {
		n, err := cleanName(r.Name)
		if err != nil {
			return Result{}, err
		}
		// Check the name first: a redeemed invite cannot be used again.
		if taken, err := s.nameTaken(ctx, n); err != nil {
			return Result{}, err
		} else if taken {
			return Result{}, fmt.Errorf("%w: %q", ErrConflict, n)
		}
		name = n
	}

	pairing, err := s.o.Tunnels.Pair(ctx, strings.TrimSpace(r.Invite), r.AllowOpenRelay)
	if err != nil {
		return Result{}, err
	}
	key, err := cleanKey(KindLocalAI, pairing.Key)
	if err != nil {
		return Result{}, err
	}
	if name == "" {
		if name, err = s.freeName(ctx, pairing.ServerName); err != nil {
			return Result{}, err
		}
	}
	id := s.newID()
	port, err := s.o.Tunnels.Open(Tunnel{ID: id, Peer: pairing.Peer, Addrs: pairing.Addrs, Relays: pairing.Relays, Hints: pairing.Hints})
	if err != nil {
		return Result{}, fmt.Errorf("the invite was redeemed, but the local port could not be opened; remove this server on the other side and use a new invite: %w", err)
	}
	sealed, err := s.sealKey(ctx, id, key)
	if err != nil {
		s.o.Tunnels.Close(id)
		return Result{}, err
	}
	rec := record{
		ID: id, Name: name, Kind: KindLocalAI, URL: p2pScheme + pairing.Peer, Status: StatusNever,
		Created: s.now(), Sealed: sealed, Transport: TransportP2P,
		P2P: &p2pRecord{Peer: pairing.Peer, Addrs: pairing.Addrs, Relays: pairing.Relays, Hints: pairing.Hints, Port: port},
	}
	if err := s.o.Store.Create(ctx, rec); err != nil {
		s.o.Tunnels.Close(id)
		if errors.Is(err, ErrConflict) {
			return Result{}, fmt.Errorf("%w: %q", ErrConflict, name)
		}
		return Result{}, err
	}
	res, err := s.finishCreate(ctx, id, r.Models)
	res.Phrase = pairing.Phrase
	return res, err
}

// nameTaken says whether a remote with that name exists, compared without
// case as the stores do.
func (s *service) nameTaken(ctx context.Context, name string) (bool, error) {
	all, err := s.o.Store.List(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range all {
		if nameKey(r.Name) == nameKey(name) {
			return true, nil
		}
	}
	return false, nil
}

// freeName derives the name of a remote added by invite from the name the
// server gave, with a number added when it is taken.
func (s *service) freeName(ctx context.Context, server string) (string, error) {
	base, err := cleanName(server)
	if err != nil {
		base = defaultP2PName
	}
	for i := 1; i <= 100; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s (%d)", base, i)
		}
		if name, err = cleanName(name); err != nil {
			continue
		}
		taken, err := s.nameTaken(ctx, name)
		if err != nil {
			return "", err
		}
		if !taken {
			return name, nil
		}
	}
	return "", invalid("Enter a name for the remote.")
}

// StartTunnels opens the forwarder of every remote over P2P that the store
// holds. Call it once at startup, before the first refresh. It does nothing
// for a service without Tunnels.
func StartTunnels(ctx context.Context, svc Service) {
	s, ok := svc.(*service)
	if !ok || s.o.Tunnels == nil {
		return
	}
	all, err := s.o.Store.List(ctx)
	if err != nil {
		xlog.Warn("remotes: could not list remotes to open their P2P tunnels", "error", err)
		return
	}
	for _, r := range all {
		if r.Transport != TransportP2P || r.P2P == nil {
			continue
		}
		if _, err := s.o.Tunnels.Open(r.tunnel()); err != nil {
			xlog.Warn("remotes: could not open a P2P tunnel", "remote", r.Name, "error", err)
		}
	}
}

// MaxP2PAddrs is how many direct addresses of a remote over P2P the store
// keeps. It is the number of addresses an invite link can carry.
const MaxP2PAddrs = 4

var errAddrsUnchanged = errors.New("remotes: the addresses did not change")

// mergeAddr returns stored with addr first. An address that is already
// stored moves to the front and is not repeated. At most max addresses stay:
// the ones that were learned longest ago drop out first, so an address of the
// invite stays until newer ones push it out. A circuit address is never a
// dial address and is refused.
func mergeAddr(stored []string, addr string, max int) []string {
	if addr == "" || strings.Contains(addr, "/p2p-circuit") || max <= 0 {
		return stored
	}
	out := make([]string, 0, min(len(stored)+1, max))
	out = append(out, addr)
	for _, a := range stored {
		if len(out) == max {
			break
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// LearnAddrs records addrs as the newest direct addresses of the remote over
// P2P with the given id, the first of addrs first. Call it when a connection
// to the remote's peer works: the addresses that worked are the best guess
// for the next dial, for example after the peer got a new IP address. It
// returns the addresses that are now stored and whether it wrote anything; it
// writes nothing when they are already first. It does nothing for a service
// without Tunnels, and fails with ErrNotFound for an id that is not a remote
// over P2P.
func LearnAddrs(ctx context.Context, svc Service, id string, addrs []string) ([]string, bool, error) {
	s, ok := svc.(*service)
	if !ok || s.o.Tunnels == nil {
		return nil, false, nil
	}
	var stored []string
	rec, err := s.o.Store.Update(ctx, id, func(r *record) error {
		if r.Transport != TransportP2P || r.P2P == nil {
			return ErrNotFound
		}
		merged := r.P2P.Addrs
		for _, a := range slices.Backward(addrs) {
			merged = mergeAddr(merged, a, MaxP2PAddrs)
		}
		if slices.Equal(merged, r.P2P.Addrs) {
			stored = slices.Clone(r.P2P.Addrs)
			return errAddrsUnchanged
		}
		r.P2P.Addrs = merged
		return nil
	})
	if errors.Is(err, errAddrsUnchanged) {
		return stored, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return slices.Clone(rec.P2P.Addrs), true, nil
}
