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
	// WarnNoRelay: the link names no relay.
	WarnNoRelay Warning = "no_relay"
	// WarnRelayUnreachable: a relay is named but no pass could be registered.
	WarnRelayUnreachable Warning = "relay_unreachable"
	// WarnNoPublicAddr: the link has no relay and no direct address.
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
	// Addrs returns the direct addresses to put in a link (at most two are
	// used). May be nil.
	Addrs func() []ma.Multiaddr
	// Relays lists the relays an invite may name. May be nil.
	Relays RelaySource
	// Registrar registers relay passes. May be nil: no invite gets a pass.
	Registrar PassRegistrar
	// Issuer issues the keys of remote invites. May be nil; remote invites
	// are then refused.
	Issuer ClientKeyIssuer
	// Limiter limits wrong codes. Nil means NewLimiter(Now).
	Limiter Limiter
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
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
	if s.cfg.Addrs != nil {
		link.Addrs = slices.Clone(s.cfg.Addrs())
		if len(link.Addrs) > maxLinkAddrs {
			link.Addrs = link.Addrs[:maxLinkAddrs]
		}
	}
	var warns []Warning
	for i, r := range relays {
		ai, err := peer.AddrInfoFromP2pAddr(r.Addr)
		if err != nil {
			continue
		}
		link.Relays = append(link.Relays, *ai)
		if r.Open {
			link.Open = append(link.Open, i)
		}
	}
	info := PassInfo{Registered: pass != "", Reason: reason}
	if pass != "" {
		info.RelayID, info.Label, info.Open = passRelay.ID, passRelay.Label, passRelay.Open
	}
	if len(link.Open) > 0 {
		warns = append(warns, WarnOpenRelay)
	}
	if len(link.Relays) == 0 {
		warns = append(warns, WarnNoRelay)
		if len(link.Addrs) == 0 {
			warns = append(warns, WarnNoPublicAddr)
		}
	} else if pass == "" && reason == string(WarnRelayUnreachable) {
		warns = append(warns, WarnRelayUnreachable)
	}
	return &Minted{Invite: inv, Link: link, Pass: info, Warnings: warns}, nil
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
	params := RedeemParams{
		Code: req.Code, Peer: from, Label: req.Name, Now: s.cfg.Now(), DeviceID: uuid.NewString(),
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
	return &RedeemResult{
		Device: dev, Kind: dev.Kind, ClientKey: key,
		Phrase: Phrase(s.cfg.ServerPeer, from, inv.ID),
	}, nil
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
