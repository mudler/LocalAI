package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/internal"
)

// p2pService implements the HTTP service of the P2P layer.
type p2pService struct{ n *p2pNet }

var _ p2papi.Service = p2pService{}

// share returns the running host of mode share, or the error that names the
// mode in effect.
func (s p2pService) share() (*p2pRuntime, error) {
	rt, mode := s.n.runtime()
	if rt == nil || mode != config.P2PModeShare {
		return nil, &p2papi.ModeError{Mode: string(mode), Need: "share"}
	}
	return rt, nil
}

func (s p2pService) Status(ctx context.Context) (p2papi.Status, error) {
	s.n.mu.Lock()
	rt, mode, lastErr := s.n.rt, s.n.mode, s.n.lastErr
	s.n.mu.Unlock()
	st := p2papi.Status{
		Mode: string(mode), Enabled: mode != config.P2PModeOff, Error: lastErr,
		Addrs: []string{}, Reservations: []string{}, HintRelays: []string{}, Reachability: "unknown",
	}
	if rt == nil {
		return st, nil
	}
	st.Running = true
	st.PeerID = rt.host.ID().String()
	reserved := map[string]bool{}
	for _, a := range rt.host.Addrs() {
		// A host with no listener still lists the bare /p2p/<id> that the
		// relay transport yields; it is no address anyone can dial.
		if ps := a.Protocols(); len(ps) == 0 || ps[0].Code == p2pnet.ProtocolP2P {
			continue
		}
		st.Addrs = append(st.Addrs, a.String())
		if r := circuitRelay(a); r != "" && !reserved[r] {
			reserved[r] = true
			st.Reservations = append(st.Reservations, r)
		}
	}
	for _, h := range rt.publicReservations(0) {
		st.HintRelays = append(st.HintRelays, h.ID.String())
	}
	switch rt.host.Reachability() {
	case p2pnet.ReachabilityPublic:
		st.Reachability = "public"
	case p2pnet.ReachabilityPrivate:
		st.Reachability = "private"
	}
	st.PublicDHT = p2papi.PublicDHTInfo{On: !rt.cfg.DisablePublicDHT, Peers: len(rt.host.Libp2p().Network().Peers())}
	if mode == config.P2PModeShare {
		st.RelayService = rt.relayServiceInfo()
		st.PendingInvites = int(rt.pending.Load())
		st.Devices = len(rt.pairedPeerStrings())
	} else {
		st.RelayService = p2papi.RelayServiceInfo{Reason: "not_share"}
	}
	return st, nil
}

// circuitRelay returns the relay peer ID of a /p2p-circuit address.
func circuitRelay(a p2pnet.Multiaddr) string {
	if !isCircuit(a) {
		return ""
	}
	v, err := a.ValueForProtocol(p2pnet.ProtocolP2P)
	if err != nil {
		return ""
	}
	return v
}

func isCircuit(a p2pnet.Multiaddr) bool {
	_, err := a.ValueForProtocol(p2pnet.ProtocolCircuit)
	return err == nil
}

// pairErr maps the errors of the pairing service to the API errors.
func pairErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, p2ppair.ErrNotFound):
		return p2papi.ErrNotFound
	case errors.Is(err, p2ppair.ErrInviteInvalid):
		return p2papi.ErrInviteInvalid
	case errors.Is(err, p2ppair.ErrRateLimited):
		return p2papi.ErrRateLimited
	case errors.Is(err, p2ppair.ErrMalformed):
		return fmt.Errorf("%w: %s", p2papi.ErrInvalid, err.Error())
	case errors.Is(err, p2ppair.ErrNotPending):
		return fmt.Errorf("%w: the invite is no longer pending", p2papi.ErrConflict)
	case errors.Is(err, p2ppair.ErrPeerPaired):
		return fmt.Errorf("%w: this device is already paired", p2papi.ErrConflict)
	case errors.Is(err, p2ppair.ErrNoKeyIssuer):
		return fmt.Errorf("%w: remote invites need authentication", p2papi.ErrConflict)
	case errors.Is(err, p2ppair.ErrDeviceLimit):
		return fmt.Errorf("%w: the owner has reached the device limit", p2papi.ErrConflict)
	}
	return err
}

func (s p2pService) MintInvite(ctx context.Context, by string, req p2papi.MintRequest) (p2papi.MintResult, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.MintResult{}, err
	}
	if req.TTLSeconds < 0 || time.Duration(req.TTLSeconds)*time.Second > p2ppair.MaxTTL {
		return p2papi.MintResult{}, fmt.Errorf("%w: ttl_seconds must be between 0 and %d", p2papi.ErrInvalid, int(p2ppair.MaxTTL.Seconds()))
	}
	if req.AllModels && len(req.Models) > 0 {
		return p2papi.MintResult{}, fmt.Errorf("%w: all_models cannot go with a list of models", p2papi.ErrInvalid)
	}
	if p2ppair.Kind(req.Kind) == p2ppair.KindRemote {
		if err := s.n.app.checkRemoteMint(ctx, by, req.Models); err != nil {
			return p2papi.MintResult{}, err
		}
	}
	if p2ppair.Kind(req.Kind) == p2ppair.KindSplit {
		if req.Labels, err = rt.splitInviteLabels(ctx, req.Labels); err != nil {
			return p2papi.MintResult{}, err
		}
	}
	mr := p2ppair.MintRequest{
		OwnerID: by, CreatedBy: by, Kind: p2ppair.Kind(req.Kind), Label: req.Label,
		TTL:            time.Duration(req.TTLSeconds) * time.Second,
		Scope:          p2ppair.Scope{Models: req.Models, AllModels: req.AllModels, Features: req.Features, Labels: req.Labels},
		AllowOpenRelay: req.AllowOpenRelay,
		OmitLANAddrs:   req.OmitLANAddrs,
		OmitRelayHints: req.OmitRelayHints,
	}
	switch req.Relays.Mode {
	case "", "auto":
		mr.Relays = p2ppair.RelayAuto
	case "none":
		mr.Relays = p2ppair.RelayNone
	default:
		mr.Relays, mr.RelayIDs = p2ppair.RelayExplicit, req.Relays.IDs
	}
	m, err := rt.pair.Mint(ctx, mr)
	if err != nil {
		return p2papi.MintResult{}, pairErr(err)
	}
	rt.reload()
	return mintResult(m), nil
}

// mintResult is the answer of a mint.
func mintResult(m *p2ppair.Minted) p2papi.MintResult {
	out := p2papi.MintResult{
		ID: m.Invite.ID, Link: m.Link.String(), Kind: string(m.Invite.Kind), ExpiresAt: m.Invite.ExpiresAt,
		QR:       m.Invite.Kind == p2ppair.KindDevice || m.Invite.Kind == p2ppair.KindRemote,
		Relay:    p2papi.RelayPass{ID: m.Pass.RelayID, Label: m.Pass.Label, Pass: m.Pass.Registered, Open: m.Pass.Open, Reason: m.Pass.Reason},
		Hints:    len(m.Link.Hints),
		Warnings: []string{},
	}
	for _, w := range m.Warnings {
		out.Warnings = append(out.Warnings, string(w))
	}
	return out
}

func (s p2pService) ListInvites(ctx context.Context) ([]p2papi.InviteView, error) {
	rt, err := s.share()
	if err != nil {
		return nil, err
	}
	invs, err := rt.pair.ListInvites(ctx)
	if err != nil {
		return nil, err
	}
	return rt.inviteViews(invs, func(p2ppair.Invite) bool { return true }), nil
}

// inviteViews converts the invites that keep accepts.
func (rt *p2pRuntime) inviteViews(invs []p2ppair.Invite, keep func(p2ppair.Invite) bool) []p2papi.InviteView {
	relays := map[string]string{}
	for _, r := range rt.relayList() {
		relays[r.ID] = r.Label
	}
	now := time.Now()
	out := make([]p2papi.InviteView, 0, len(invs))
	for _, i := range invs {
		if !keep(i) {
			continue
		}
		v := p2papi.InviteView{
			ID: i.ID, Kind: string(i.Kind), Label: i.Label, CreatedAt: i.CreatedAt, CreatedBy: i.CreatedBy,
			ExpiresAt: i.ExpiresAt, Status: string(i.Status(now)), DeviceID: i.DeviceID,
		}
		v.Models, v.AllModels = modelGrant(i.Kind, i.Scope)
		if i.PassRelayID != "" {
			v.RelayPass = relays[i.PassRelayID]
			if v.RelayPass == "" {
				v.RelayPass = i.PassRelayID
			}
		}
		// The admin compares the phrase with the one the device shows, so a
		// used invite carries it, computed the way the redeem did.
		if p, err := p2pnet.DecodePeerID(i.UsedByPeer); i.UsedByPeer != "" && err == nil {
			v.DevicePeerID = i.UsedByPeer
			v.Phrase = p2ppair.Phrase(rt.host.ID(), p, i.ID)
		}
		out = append(out, v)
	}
	return out
}

func (s p2pService) CancelInvite(ctx context.Context, id string) error {
	rt, err := s.share()
	if err != nil {
		return err
	}
	if _, err := rt.pair.Cancel(ctx, id, "admin"); err != nil {
		return pairErr(err)
	}
	rt.reload()
	return nil
}

func (s p2pService) ListDevices(ctx context.Context, owner string) ([]p2papi.DeviceView, error) {
	rt, err := s.share()
	if err != nil {
		return nil, err
	}
	var devs []p2ppair.Device
	if owner != "" {
		devs, err = rt.pair.ListDevicesByOwner(ctx, owner)
	} else {
		devs, err = rt.pair.ListDevices(ctx)
	}
	if err != nil {
		return nil, err
	}
	return rt.deviceViews(ctx, devs), nil
}

// deviceViews converts devices, with the path of the connected ones and the
// names of their owners.
func (rt *p2pRuntime) deviceViews(ctx context.Context, devs []p2ppair.Device) []p2papi.DeviceView {
	labels := rt.relayLabels()
	names := rt.net.app.userNames(ctx, devs)
	out := make([]p2papi.DeviceView, 0, len(devs))
	for _, d := range devs {
		v := p2papi.DeviceView{
			ID: d.ID, Kind: string(d.Kind), Label: d.Label, PeerID: d.PeerID, CreatedAt: d.CreatedAt,
			LastSeenAt: d.LastSeenAt, Status: string(d.Status()), Path: p2papi.PathView{Kind: string(p2pnet.PathNone)},
			OwnerID: d.OwnerID, OwnerName: names[d.OwnerID], SignIn: d.SignIn,
		}
		v.Models, v.AllModels = modelGrant(d.Kind, d.Scope)
		if d.LastPath != "" {
			v.Path.Kind = d.LastPath
		}
		if p, err := p2pnet.DecodePeerID(d.PeerID); err == nil && d.RevokedAt == nil {
			if path := rt.host.Path(p); path.Kind != p2pnet.PathNone {
				v.Online = true
				v.Path = pathView(path, labels)
			}
		}
		out = append(out, v)
	}
	return out
}

// modelGrant reads what a remote invite or device grants: its list, or every
// model. An old invite with no list grants every model, so it reads the same
// as one made with all_models. Other kinds grant no models.
func modelGrant(kind p2ppair.Kind, s p2ppair.Scope) (models []string, all bool) {
	if kind != p2ppair.KindRemote {
		return nil, false
	}
	if len(s.Models) > 0 {
		return s.Models, false
	}
	return nil, true
}

func pathView(p p2pnet.Path, labels map[string]string) p2papi.PathView {
	v := p2papi.PathView{
		Kind: string(p.Kind), Limited: p.Limited, LimitBytes: p.LimitBytes,
		LimitSeconds: int64(p.LimitDuration / time.Second), RTTMillis: p.RTT.Milliseconds(),
	}
	if p.Relay != "" {
		v.RelayLabel = labels[p.Relay.String()]
		if v.RelayLabel == "" {
			v.RelayLabel = p.Relay.String()
		}
	}
	return v
}

func (s p2pService) RenameDevice(ctx context.Context, id, label string) error {
	rt, err := s.share()
	if err != nil {
		return err
	}
	return pairErr(rt.pair.RenameDevice(ctx, id, label))
}

// RevokeDevice revokes the device row and its client key, ends the web
// sessions the device opened, removes the peer from the gate and closes its
// connections, tells the relays, and tells the other frontends.
func (s p2pService) RevokeDevice(ctx context.Context, id, by string) error {
	rt, err := s.share()
	if err != nil {
		return err
	}
	dev, err := rt.pair.GetDevice(ctx, id)
	if err != nil {
		return pairErr(err)
	}
	revokeErr := rt.revokeDevice(ctx, dev, by)
	rt.changed("revoke")
	return pairErr(revokeErr)
}

// relayManager returns the relay manager of mode share.
func (s p2pService) relayManager() (relayManager, error) {
	rt, err := s.share()
	if err != nil {
		return nil, err
	}
	if rt.relays == nil {
		return nil, fmt.Errorf("%w: this server has no relay manager", p2papi.ErrUnavailable)
	}
	return rt.relays, nil
}

func (s p2pService) ListRelays(context.Context) ([]p2papi.Relay, error) {
	rt, err := s.share()
	if err != nil {
		return nil, err
	}
	return rt.relayList(), nil
}

func (s p2pService) AddRelay(ctx context.Context, addr, label, claimCode string) (p2papi.Relay, error) {
	if _, err := p2pnet.ParsePeerAddr(addr); err != nil {
		return p2papi.Relay{}, fmt.Errorf("%w: %s", p2papi.ErrInvalid, err.Error())
	}
	rm, err := s.relayManager()
	if err != nil {
		return p2papi.Relay{}, err
	}
	r, err := rm.Add(ctx, addr, label, claimCode)
	if err == nil {
		s.restartForRelays()
	}
	return r, err
}

func (s p2pService) RemoveRelay(ctx context.Context, id string) error {
	rm, err := s.relayManager()
	if err != nil {
		return err
	}
	if err := rm.Remove(ctx, id); err != nil {
		return err
	}
	s.restartForRelays()
	return nil
}

// restartForRelays restarts the host after the relay set changed: its static
// relays are fixed when it starts. It runs after the answer is sent, so the
// request is not cut by its own restart.
func (s p2pService) restartForRelays() {
	go func() {
		if err := s.n.restart(); err != nil {
			xlog.Error("P2P: could not restart the host after the relays changed", "error", err)
		}
	}()
}

func (s p2pService) CheckRelay(ctx context.Context, id string) (p2papi.RelayCheck, error) {
	rm, err := s.relayManager()
	if err != nil {
		return p2papi.RelayCheck{}, err
	}
	return rm.Check(ctx, id)
}

func (s p2pService) SyncRelay(ctx context.Context, id string) (p2papi.Relay, error) {
	rm, err := s.relayManager()
	if err != nil {
		return p2papi.Relay{}, err
	}
	return rm.Sync(ctx, id)
}

func (s p2pService) ClaimRelay(ctx context.Context, id, code string) (p2papi.Relay, error) {
	rm, err := s.relayManager()
	if err != nil {
		return p2papi.Relay{}, err
	}
	return rm.Claim(ctx, id, code)
}

func (s p2pService) RelayLimits(ctx context.Context, id string) (p2papi.RelayLimitsInfo, error) {
	rm, err := s.relayManager()
	if err != nil {
		return p2papi.RelayLimitsInfo{}, err
	}
	return rm.Limits(ctx, id)
}

func (s p2pService) SetRelayLimits(ctx context.Context, id string, req p2papi.SetRelayLimitsRequest) (p2papi.RelayLimitsInfo, error) {
	rm, err := s.relayManager()
	if err != nil {
		return p2papi.RelayLimitsInfo{}, err
	}
	for _, v := range []*int64{req.CircuitBytes, req.CircuitSeconds, req.RateBPS} {
		if v != nil && *v < 0 {
			return p2papi.RelayLimitsInfo{}, fmt.Errorf("%w: limits must not be negative", p2papi.ErrInvalid)
		}
	}
	return rm.SetLimits(ctx, id, req)
}

// RegenerateIdentity makes a new key (the old one is kept next to it as a
// backup) and restarts the host with it. Every paired device and remote knows
// the old peer ID, so each must pair again.
func (s p2pService) RegenerateIdentity(ctx context.Context) (p2papi.Status, error) {
	rt, mode := s.n.runtime()
	if rt == nil {
		return p2papi.Status{}, &p2papi.ModeError{Mode: string(mode), Need: "on"}
	}
	cfg := s.n.app.ApplicationConfig()
	path, err := config.ResolveP2PIdentityPath(cfg.P2P.IdentityPath, cfg.DataPath)
	if err != nil {
		return p2papi.Status{}, err
	}
	if _, err := p2pnet.RegenerateIdentity(path); err != nil {
		return p2papi.Status{}, err
	}
	if err := s.n.restart(); err != nil {
		return p2papi.Status{}, err
	}
	return s.Status(ctx)
}

func (s p2pService) Redeem(ctx context.Context, from string, req p2papi.RedeemRequest) (p2papi.RedeemResult, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.RedeemResult{}, err
	}
	p, err := p2pnet.DecodePeerID(from)
	if err != nil {
		return p2papi.RedeemResult{}, fmt.Errorf("%w: unknown peer", p2papi.ErrInvalid)
	}
	res, err := rt.pair.Redeem(ctx, p, p2ppair.RedeemRequest{
		V: req.V, Code: req.Code, Name: req.Name, Platform: req.Platform, Source: rt.sourceOf(p),
	})
	if err != nil {
		return p2papi.RedeemResult{}, pairErr(err)
	}
	rt.changed("pair")
	if rt.relays != nil {
		rt.relays.PeerAdded(p.String())
	}
	name := s.n.app.ApplicationConfig().Branding.InstanceName
	if name == "" {
		name = "LocalAI"
	}
	out := p2papi.RedeemResult{
		DeviceID: res.Device.ID, Kind: string(res.Kind), Phrase: res.Phrase,
		Server:           p2papi.ServerInfo{PeerID: rt.host.ID().String(), Name: name, Version: internal.Version},
		DeviceCredential: res.DeviceCredential, SignIn: res.SignIn, OwnerName: res.OwnerName,
	}
	if res.ClientKey != nil {
		out.ClientKey = &p2papi.ClientKey{ID: res.ClientKey.ID, Secret: res.ClientKey.Secret}
	}
	return out, nil
}

// Routes answers a paired device with the current routes to this server.
// httpHandler already refused a peer that is not paired; the check here
// keeps the route safe if it is ever served from somewhere else.
func (s p2pService) Routes(ctx context.Context, from string) (p2papi.Routes, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.Routes{}, err
	}
	p, err := p2pnet.DecodePeerID(from)
	if err != nil {
		return p2papi.Routes{}, fmt.Errorf("%w: unknown peer", p2papi.ErrInvalid)
	}
	if !rt.paired(p) {
		return p2papi.Routes{}, fmt.Errorf("%w: this peer is not paired", p2papi.ErrForbidden)
	}
	r, err := rt.pair.Routes(ctx, p)
	if err != nil {
		return p2papi.Routes{}, err
	}
	out := p2papi.Routes{Server: rt.host.ID().String(), Addrs: []string{}, Relays: []string{}, Hints: []string{}}
	for _, a := range r.Addrs {
		out.Addrs = append(out.Addrs, a.String())
	}
	out.Relays = append(out.Relays, p2pAddrs(r.Relays)...)
	out.Hints = append(out.Hints, p2pAddrs(r.Hints)...)
	return out, nil
}

// p2pAddrs writes each address of each relay with its /p2p/ part, as a link
// does.
func p2pAddrs(infos []p2pnet.AddrInfo) []string {
	var out []string
	for _, ai := range infos {
		for _, a := range ai.Addrs {
			out = append(out, a.String()+"/p2p/"+ai.ID.String())
		}
	}
	return out
}

// sourceOf is the network source of a peer for the per-source limit of wrong
// codes: the IP address of a direct connection, the relay of a relayed one.
func (rt *p2pRuntime) sourceOf(p p2pnet.ID) string {
	for _, c := range rt.host.Libp2p().Network().ConnsToPeer(p) {
		a := c.RemoteMultiaddr()
		if r := circuitRelay(a); r != "" {
			return "relay:" + r
		}
		if ip, err := p2pnet.AddrIP(a); err == nil {
			return ip.String()
		}
	}
	return ""
}
