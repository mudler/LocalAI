package p2ppair

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
)

// Time limits of an invite.
const (
	// DefaultTTL is the life of an invite when the request names none.
	DefaultTTL = 600 * time.Second
	// MaxTTL is the longest life of an invite.
	MaxTTL = 24 * time.Hour
)

// Warning names a condition that the administrator should see after a mint.
type Warning string

// The warnings of a mint.
const (
	// WarnOpenRelay: the link names an open relay.
	WarnOpenRelay Warning = "open_relay"
	// WarnNoRelay: the link names no relay and no relay hint.
	WarnNoRelay Warning = "no_relay"
	// WarnPunchOnly: the link names no relay that carries data, only relay
	// hints. A device reaches the server only when a hole punch succeeds.
	// It replaces WarnNoRelay.
	WarnPunchOnly Warning = "punch_only"
	// WarnRelayUnreachable: a relay is named but no pass could be registered.
	WarnRelayUnreachable Warning = "relay_unreachable"
	// WarnNoPublicAddr: the link has no carrier relay and no public direct
	// address.
	// It may still carry local network addresses.
	WarnNoPublicAddr Warning = "no_public_addr"
)

// RelayMode selects how the relays of an invite are chosen.
type RelayMode string

// The relay modes.
const (
	// RelayAuto uses the relays the server knows (open ones only when
	// MintRequest.AllowOpenRelay is set).
	RelayAuto RelayMode = "auto"
	// RelayNone names no relay.
	RelayNone RelayMode = "none"
	// RelayExplicit uses the relays in MintRequest.RelayIDs.
	RelayExplicit RelayMode = "explicit"
)

// Config configures a Service. ServerPeer is required.
type Config struct {
	// ServerPeer is the peer ID of this server; it goes into links and
	// phrases.
	ServerPeer peer.ID
	// Addrs returns the public direct addresses to put in a link (at most
	// maxPublicAddrs are used). May be nil.
	Addrs func() []ma.Multiaddr
	// LANAddrs returns the addresses of the local networks of the server.
	// A link carries them after the public ones, unless the request sets
	// OmitLANAddrs. May be nil.
	LANAddrs func() []ma.Multiaddr
	// Relays lists the relays an invite may name. May be nil.
	Relays RelaySource
	// Hints returns the public relays on which the server holds a
	// reservation, each with one transport address. A link names at most
	// maxLinkHints of them, unless the request sets OmitRelayHints; relays
	// that Relays lists are left out, because they go in the link as
	// carriers. May be nil: links carry no hints.
	Hints func() []peer.AddrInfo
	// Registrar registers relay passes. May be nil: no invite gets a pass.
	Registrar PassRegistrar
	// Issuer issues the keys of remote invites. May be nil; remote invites
	// are then refused.
	Issuer ClientKeyIssuer
	// Limiter limits wrong codes. Nil means NewLimiter(Now).
	Limiter Limiter
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// HMACSecret keys the hash of the device credentials. Give it the secret
	// that the store hashes the codes with.
	HMACSecret string
	// OwnerName returns the display name of the owner of a sign-in invite,
	// for the redeem result. May be nil: the name is empty.
	OwnerName func(ctx context.Context, ownerID string) string
	// DeviceLimit returns how many active sign-in devices one owner may
	// have; zero or less means no limit. It is read at each redeem of a
	// sign-in invite. May be nil: no limit.
	DeviceLimit func(ctx context.Context) int
}

// Service is the pairing service.
type Service struct {
	st  Store
	cfg Config
}

// NewService returns a Service on st.
func NewService(st Store, cfg Config) (*Service, error) {
	if st == nil {
		return nil, errors.New("p2ppair: nil store")
	}
	if cfg.ServerPeer == "" {
		return nil, errors.New("p2ppair: the server peer ID is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Limiter == nil {
		cfg.Limiter = NewLimiter(cfg.Now)
	}
	return &Service{st: st, cfg: cfg}, nil
}

// MintRequest describes an invite to create.
type MintRequest struct {
	OwnerID   string
	CreatedBy string
	Kind      Kind
	Label     string
	// TTL is the life of the invite; zero means DefaultTTL. A value above
	// MaxTTL is refused.
	TTL   time.Duration
	Scope Scope
	// Relays selects the relays; the zero value means RelayAuto.
	Relays RelayMode
	// RelayIDs names the relays for RelayExplicit.
	RelayIDs []string
	// AllowOpenRelay lets the invite name open relays.
	AllowOpenRelay bool
	// OmitLANAddrs keeps the local network addresses of the server out of
	// the link. They reveal the layout of that network to whoever holds the
	// link, but they let a device on the same network redeem it with no
	// relay and no multicast discovery.
	OmitLANAddrs bool
	// OmitRelayHints keeps the relay hints out of the link. A hint tells the
	// holder of the link which public relay the server uses, not its
	// address; leaving them out gives a shorter link and QR code. RelayNone
	// keeps them, because a hint carries no data.
	OmitRelayHints bool
	// SignIn gives the device a credential at redeem time, to sign in as the
	// owner. Only the device kind takes it.
	SignIn bool
}

// PassInfo says what happened to the relay pass of a mint.
type PassInfo struct {
	// RelayID and Label name the relay that holds the pass; empty when none.
	RelayID string `json:"id,omitempty"`
	Label   string `json:"label,omitempty"`
	// Registered is true when the link carries a pass.
	Registered bool `json:"pass"`
	// Open is true when the relay is an open one.
	Open bool `json:"open"`
	// Reason says why there is no pass: "relay_unreachable" or "no_relay".
	Reason string `json:"reason,omitempty"`
}

// Minted is the result of Mint.
type Minted struct {
	Invite Invite
	// Link is the invite link to share.
	Link *Link
	// Pass says whether a relay pass is in the link, or why not.
	Pass     PassInfo
	Warnings []Warning
}

// Mint creates an invite and its link. A relay that cannot register the pass
// is not an error: the invite is created without a pass and Minted.Pass says
// why.
func (s *Service) Mint(ctx context.Context, req MintRequest) (*Minted, error) {
	if !req.Kind.Valid() {
		return nil, fmt.Errorf("%w: unknown kind %q", ErrMalformed, req.Kind)
	}
	if req.Kind == KindRemote && s.cfg.Issuer == nil {
		return nil, ErrNoKeyIssuer
	}
	if req.SignIn && req.Kind != KindDevice {
		return nil, fmt.Errorf("%w: sign_in needs the device kind", ErrMalformed)
	}
	if !validLabel(req.Label) {
		return nil, fmt.Errorf("%w: label", ErrMalformed)
	}
	ttl := req.TTL
	switch {
	case ttl == 0:
		ttl = DefaultTTL
	case ttl < 0 || ttl > MaxTTL:
		return nil, fmt.Errorf("%w: ttl", ErrMalformed)
	}
	relays, err := s.selectRelays(ctx, req)
	if err != nil {
		return nil, err
	}
	code, err := newCode()
	if err != nil {
		return nil, err
	}
	now := s.cfg.Now().UTC()
	inv := Invite{
		ID: uuid.NewString(), OwnerID: req.OwnerID, Kind: req.Kind, Label: req.Label,
		Scope: req.Scope, ExpiresAt: now.Add(ttl).Truncate(time.Second), CreatedBy: req.CreatedBy, CreatedAt: now,
		OmitLANAddrs: req.OmitLANAddrs, SignIn: req.SignIn,
	}
	for _, r := range relays {
		inv.RelayIDs = append(inv.RelayIDs, r.ID)
	}

	pass, passRelay, reason := s.registerPass(ctx, inv, code, ttl, relays)
	if pass != "" {
		inv.PassRelayID = passRelay.ID
	}
	if err := s.st.CreateInvite(ctx, inv, code); err != nil {
		if pass != "" {
			// The store error is the one the caller needs; cancelPass logs
			// its own failure and the pass expires with its TTL anyway.
			_ = s.cancelPass(ctx, passRelay, inv.ID)
		}
		return nil, err
	}

	link := &Link{
		V: linkVersion, Kind: inv.Kind, Peer: s.cfg.ServerPeer, Code: code,
		Expires: inv.ExpiresAt, Label: inv.Label, Pass: pass,
	}
	var public int
	link.Addrs, public = s.directAddrs(req.OmitLANAddrs)
	var warns []Warning
	link.Relays, link.Open = relayInfos(relays)
	if !req.OmitRelayHints {
		link.Hints = s.relayHints(ctx, link.Relays)
	}
	info := PassInfo{Registered: pass != "", Reason: reason}
	if pass != "" {
		info.RelayID, info.Label, info.Open = passRelay.ID, passRelay.Label, passRelay.Open
	}
	if len(link.Open) > 0 {
		warns = append(warns, WarnOpenRelay)
	}
	if len(link.Relays) == 0 {
		if len(link.Hints) > 0 {
			warns = append(warns, WarnPunchOnly)
		} else {
			warns = append(warns, WarnNoRelay)
		}
		if public == 0 {
			warns = append(warns, WarnNoPublicAddr)
		}
	} else if pass == "" && reason == string(WarnRelayUnreachable) {
		warns = append(warns, WarnRelayUnreachable)
	}
	return &Minted{Invite: inv, Link: link, Pass: info, Warnings: warns}, nil
}

// directAddrs returns the direct addresses a link carries: at most
// maxPublicAddrs public ones, then the local network ones unless omitLAN is
// set, up to maxLinkAddrs in all. public is how many are public.
func (s *Service) directAddrs(omitLAN bool) (addrs []ma.Multiaddr, public int) {
	if s.cfg.Addrs != nil {
		addrs = slices.Clone(s.cfg.Addrs())
		if len(addrs) > maxPublicAddrs {
			addrs = addrs[:maxPublicAddrs]
		}
	}
	public = len(addrs)
	if s.cfg.LANAddrs != nil && !omitLAN {
		addrs = appendAddrs(addrs, s.cfg.LANAddrs(), maxLinkAddrs)
	}
	return addrs, public
}

// relayInfos converts relays to the relays of a link and the indexes of the
// open ones among them. A relay whose address does not parse is left out.
func relayInfos(relays []Relay) (infos []peer.AddrInfo, open []int) {
	for _, r := range relays {
		ai, err := peer.AddrInfoFromP2pAddr(r.Addr)
		if err != nil {
			continue
		}
		if r.Open {
			open = append(open, len(infos))
		}
		infos = append(infos, *ai)
	}
	return infos, open
}

// relayHints returns the hints a link carries: at most maxLinkHints relays
// from Config.Hints, each with its first address. It leaves out the server,
// the carriers of the link, every relay the relay source knows (a gated
// relay admits a device only with a pass, so it is a carrier or nothing),
// repeats, and addresses too long for the link.
func (s *Service) relayHints(ctx context.Context, carriers []peer.AddrInfo) []peer.AddrInfo {
	if s.cfg.Hints == nil {
		return nil
	}
	skip := map[peer.ID]bool{s.cfg.ServerPeer: true}
	for _, c := range carriers {
		skip[c.ID] = true
	}
	if s.cfg.Relays != nil {
		for _, r := range s.cfg.Relays.Relays(ctx) {
			if ai, err := peer.AddrInfoFromP2pAddr(r.Addr); err == nil {
				skip[ai.ID] = true
			}
		}
	}
	var out []peer.AddrInfo
	for _, h := range s.cfg.Hints() {
		if len(out) >= maxLinkHints {
			break
		}
		if h.ID == "" || skip[h.ID] || len(h.Addrs) == 0 {
			continue
		}
		one := peer.AddrInfo{ID: h.ID, Addrs: []ma.Multiaddr{h.Addrs[0]}}
		if str, ok := relayString(one); !ok || len(str) > maxAddrLen {
			continue
		}
		skip[h.ID] = true
		out = append(out, one)
	}
	return out
}

// appendAddrs adds the addresses of more to dst, skipping duplicates and
// addresses longer than maxAddrLen, until dst holds limit addresses.
func appendAddrs(dst, more []ma.Multiaddr, limit int) []ma.Multiaddr {
	for _, a := range more {
		if len(dst) >= limit {
			break
		}
		if len(a.String()) > maxAddrLen || slices.ContainsFunc(dst, a.Equal) {
			continue
		}
		dst = append(dst, a)
	}
	return dst
}

// selectRelays resolves the relays named by the request, at most
// maxLinkRelays, with gated relays before open ones.
func (s *Service) selectRelays(ctx context.Context, req MintRequest) ([]Relay, error) {
	mode := req.Relays
	if mode == "" {
		mode = RelayAuto
	}
	if mode == RelayNone || s.cfg.Relays == nil {
		if mode == RelayExplicit && len(req.RelayIDs) > 0 {
			return nil, fmt.Errorf("%w: unknown relay", ErrMalformed)
		}
		return nil, nil
	}
	known := s.cfg.Relays.Relays(ctx)
	var out []Relay
	switch mode {
	case RelayAuto:
		for _, r := range known {
			if r.Open && !req.AllowOpenRelay {
				continue
			}
			out = append(out, r)
		}
	case RelayExplicit:
		for _, id := range req.RelayIDs {
			i := slices.IndexFunc(known, func(r Relay) bool { return r.ID == id })
			if i < 0 {
				return nil, fmt.Errorf("%w: unknown relay %q", ErrMalformed, id)
			}
			if known[i].Open && !req.AllowOpenRelay {
				return nil, fmt.Errorf("%w: relay %q is open and open relays are not allowed", ErrMalformed, id)
			}
			if !slices.ContainsFunc(out, func(r Relay) bool { return r.ID == id }) {
				out = append(out, known[i])
			}
		}
	default:
		return nil, fmt.Errorf("%w: relay mode", ErrMalformed)
	}
	slices.SortStableFunc(out, func(a, b Relay) int {
		switch {
		case a.Open == b.Open:
			return 0
		case a.Open:
			return 1
		}
		return -1
	})
	if len(out) > maxLinkRelays {
		out = out[:maxLinkRelays]
	}
	return out, nil
}

// registerPass tries the gated relays in order and returns the first pass it
// gets. It never fails: with no pass it returns the reason.
func (s *Service) registerPass(ctx context.Context, inv Invite, code string, ttl time.Duration, relays []Relay) (pass string, on Relay, reason string) {
	if s.cfg.Registrar == nil || len(relays) == 0 {
		return "", Relay{}, "no_relay"
	}
	tried := false
	for _, r := range relays {
		if r.Open {
			continue
		}
		tried = true
		p, err := s.cfg.Registrar.RegisterPass(ctx, PassRequest{Relay: r, InviteID: inv.ID, Code: code, TTL: ttl, Label: inv.Label})
		if err != nil || p == "" || len(p) > maxPassLen {
			xlog.Warn("Could not register a relay pass; the invite has no pass", "relay", r.ID, "error", err)
			continue
		}
		return p, r, ""
	}
	if !tried {
		return "", Relay{}, "no_relay"
	}
	return "", Relay{}, string(WarnRelayUnreachable)
}

func (s *Service) cancelPass(ctx context.Context, r Relay, inviteID string) error {
	if s.cfg.Registrar == nil {
		return nil
	}
	err := s.cfg.Registrar.CancelPass(ctx, r, inviteID)
	if err != nil {
		xlog.Warn("Could not cancel a relay pass", "relay", r.ID, "error", err)
	}
	return err
}

func validLabel(l string) bool {
	return len(l) <= maxLabelLen && utf8.ValidString(l) && !strings.ContainsFunc(l, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// RedeemRequest is the body of a redeem call.
type RedeemRequest struct {
	// V is the protocol version; it must be 1.
	V    int    `json:"v"`
	Code string `json:"code"`
	// Name labels the new device; it may be empty.
	Name     string `json:"name"`
	Platform string `json:"platform"`
	// Source is the network address of the dialer as the caller saw it, for
	// the per-source rate limit. It is not part of the wire body.
	Source string `json:"-"`
}

// RedeemResult is what a successful redeem returns. The caller adds the
// server's name and version.
type RedeemResult struct {
	Device Device `json:"device"`
	Kind   Kind   `json:"kind"`
	// Phrase is the confirmation phrase to show on both sides.
	Phrase string `json:"phrase"`
	// ClientKey is set for the remote kind only.
	ClientKey *IssuedKey `json:"client_key,omitempty"`
	// DeviceCredential is the sign-in credential of the device, for a
	// sign-in invite only. It is returned once: the store keeps its hash.
	DeviceCredential string `json:"device_credential,omitempty"`
	// SignIn is true when the device got a credential.
	SignIn bool `json:"sign_in,omitempty"`
	// OwnerName names the owner the device signs in as, when known.
	OwnerName string `json:"owner_name,omitempty"`
}

// Redeem redeems an invite code for the peer that sent it. A wrong, used,
// canceled or expired code all return ErrInviteInvalid. After too many wrong
// codes it returns ErrRateLimited without looking at the code.
func (s *Service) Redeem(ctx context.Context, from peer.ID, req RedeemRequest) (*RedeemResult, error) {
	if from == "" || req.V != linkVersion || len(req.Name) > maxLabelLen || !validLabel(req.Name) || len(req.Platform) > 64 || len(req.Code) > 64 {
		return nil, ErrMalformed
	}
	// The attempt counts as a wrong code from here on. It is taken back when
	// the code was right or the store failed for a reason of its own.
	ok, refund := s.cfg.Limiter.Attempt(from, req.Source)
	if !ok {
		return nil, ErrRateLimited
	}
	if !validCodeFormat(NormalizeCode(req.Code)) {
		return nil, ErrInviteInvalid
	}
	var key *IssuedKey
	// The credential is made before the store knows whether the invite is a
	// sign-in one: the store keeps its hash only for such an invite, and the
	// result returns it only then.
	credential, err := newCredential()
	if err != nil {
		refund()
		return nil, err
	}
	params := RedeemParams{
		Code: req.Code, Peer: from, Label: req.Name, Now: s.cfg.Now(), DeviceID: uuid.NewString(),
		CredentialHash: hashCredential(credential, s.cfg.HMACSecret),
	}
	if s.cfg.DeviceLimit != nil {
		params.MaxDevicesForOwner = max(s.cfg.DeviceLimit(ctx), 0)
	}
	// The key issuer is called for remote invites only; the store calls it
	// after it claimed the code, so a wrong code never reaches it.
	params.Issue = func(ctx context.Context, tx Tx, inv Invite, dev Device) (string, error) {
		if inv.Kind != KindRemote {
			return "", nil
		}
		if s.cfg.Issuer == nil {
			return "", ErrNoKeyIssuer
		}
		k, err := s.cfg.Issuer.Issue(ctx, tx, inv, dev)
		if err != nil {
			return "", fmt.Errorf("p2ppair: issue client key: %w", err)
		}
		key = &k
		return k.ID, nil
	}
	inv, dev, err := s.st.Redeem(ctx, params)
	if err != nil {
		if !errors.Is(err, ErrInviteInvalid) {
			// A store error says nothing about the code.
			refund()
		}
		return nil, err
	}
	refund()
	res := &RedeemResult{
		Device: dev, Kind: dev.Kind, ClientKey: key,
		Phrase: Phrase(s.cfg.ServerPeer, from, inv.ID),
	}
	if dev.SignIn {
		res.DeviceCredential, res.SignIn = credential, true
		if s.cfg.OwnerName != nil {
			res.OwnerName = s.cfg.OwnerName(ctx, dev.OwnerID)
		}
	}
	return res, nil
}

// VerifyDeviceCredential returns the active sign-in device of peer p when
// credential is its credential. The hashes are compared in constant time.
// Every failure returns ErrCredentialInvalid: no device for the peer, a
// revoked device, a device without sign-in and a wrong credential alike.
func (s *Service) VerifyDeviceCredential(ctx context.Context, p peer.ID, credential string) (Device, error) {
	if p == "" || credential == "" || len(credential) > 128 {
		return Device{}, ErrCredentialInvalid
	}
	d, err := s.st.GetActiveDeviceByPeer(ctx, p)
	if errors.Is(err, ErrNotFound) {
		return Device{}, ErrCredentialInvalid
	}
	if err != nil {
		return Device{}, err
	}
	want := hashCredential(credential, s.cfg.HMACSecret)
	if !d.SignIn || d.CredentialHash == "" || !codeMatches(d.CredentialHash, want) {
		return Device{}, ErrCredentialInvalid
	}
	return d, nil
}

// Revoke revokes a device and, for a remote device, its client key. The
// device row is revoked first; if the key cannot be revoked the error says so
// and the device stays revoked.
func (s *Service) Revoke(ctx context.Context, deviceID, by string) error {
	dev, err := s.st.RevokeDevice(ctx, deviceID, by, s.cfg.Now())
	if err != nil {
		return err
	}
	if dev.ClientKeyID != "" && s.cfg.Issuer != nil {
		if err := s.cfg.Issuer.Revoke(ctx, dev.ClientKeyID); err != nil {
			return fmt.Errorf("p2ppair: the device is revoked but its client key could not be: %w", err)
		}
	}
	return nil
}

// CancelResult says how the relay side of a cancel went.
type CancelResult struct {
	// PassRelayID is the relay that held a pass; empty when there was none.
	PassRelayID string
	// PassCanceled is true when that pass was dropped.
	PassCanceled bool
	// PassErr is why not, when it was not.
	PassErr error
}

// Cancel cancels a pending invite and drops its relay pass. A relay that
// cannot be reached does not undo the cancel; the result carries the error.
func (s *Service) Cancel(ctx context.Context, inviteID, by string) (*CancelResult, error) {
	inv, err := s.st.GetInvite(ctx, inviteID)
	if err != nil {
		return nil, err
	}
	if err := s.st.CancelInvite(ctx, inviteID, s.cfg.Now()); err != nil {
		return nil, err
	}
	xlog.Info("Pairing invite canceled", "invite", inviteID, "by", by)
	res := &CancelResult{PassRelayID: inv.PassRelayID}
	if inv.PassRelayID == "" {
		return res, nil
	}
	relay := Relay{ID: inv.PassRelayID}
	if s.cfg.Relays != nil {
		for _, r := range s.cfg.Relays.Relays(ctx) {
			if r.ID == inv.PassRelayID {
				relay = r
			}
		}
	}
	res.PassErr = s.cancelPass(ctx, relay, inv.ID)
	res.PassCanceled = res.PassErr == nil
	return res, nil
}

// ListInvites returns all invites, newest first. Use Invite.Status for the
// state.
func (s *Service) ListInvites(ctx context.Context) ([]Invite, error) {
	return s.st.ListInvites(ctx)
}

// ListDevices returns all devices, revoked ones included, newest first.
func (s *Service) ListDevices(ctx context.Context) ([]Device, error) {
	return s.st.ListDevices(ctx)
}

// RenameDevice changes the label of a device.
func (s *Service) RenameDevice(ctx context.Context, id, label string) error {
	if !validLabel(label) {
		return fmt.Errorf("%w: label", ErrMalformed)
	}
	return s.st.RenameDevice(ctx, id, label)
}

// SetAllowSlowPath records whether the server may use a device over a
// relayed path.
func (s *Service) SetAllowSlowPath(ctx context.Context, id string, allow bool) error {
	return s.st.SetDeviceAllowSlowPath(ctx, id, allow)
}

// SetSplitNode attaches a split device to a backend node, or to this server
// with an empty node.
func (s *Service) SetSplitNode(ctx context.Context, id, node string) error {
	return s.st.SetDeviceNode(ctx, id, node, "")
}

// DetachSplitNode attaches a split device to no node and records why.
func (s *Service) DetachSplitNode(ctx context.Context, id, note string) error {
	return s.st.SetDeviceNode(ctx, id, "", note)
}

// Touch records that an active device was seen now, and by which path.
func (s *Service) Touch(ctx context.Context, p peer.ID, path string) error {
	return s.st.TouchDevice(ctx, p, s.cfg.Now(), path)
}

// PairedPeers returns the peer IDs of the active devices. The relay sync and
// the connection gater use it as the allowlist.
func (s *Service) PairedPeers(ctx context.Context) ([]peer.ID, error) {
	devs, err := s.st.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	var out []peer.ID
	for _, d := range devs {
		if d.RevokedAt != nil {
			continue
		}
		id, err := parsePeer(d.PeerID)
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// ListInvitesByOwner returns the invites of one owner, newest first.
func (s *Service) ListInvitesByOwner(ctx context.Context, ownerID string) ([]Invite, error) {
	return s.st.ListInvitesByOwner(ctx, ownerID)
}

// ListDevicesByOwner returns the devices of one owner, revoked ones included,
// newest first.
func (s *Service) ListDevicesByOwner(ctx context.Context, ownerID string) ([]Device, error) {
	return s.st.ListDevicesByOwner(ctx, ownerID)
}

// GetDevice returns one device or ErrNotFound.
func (s *Service) GetDevice(ctx context.Context, id string) (Device, error) {
	return s.st.GetDevice(ctx, id)
}

// GetInvite returns one invite or ErrNotFound.
func (s *Service) GetInvite(ctx context.Context, id string) (Invite, error) {
	return s.st.GetInvite(ctx, id)
}
