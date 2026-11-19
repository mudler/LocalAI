// Package p2p holds the HTTP handlers of the P2P layer: status, invites,
// devices, relays, the identity, and the redeem route that is served only over
// P2P. The handlers take the Service interface; the application implements it.
package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Service is what the handlers need from the P2P layer.
type Service interface {
	Status(ctx context.Context) (Status, error)
	MintInvite(ctx context.Context, by string, req MintRequest) (MintResult, error)
	ListInvites(ctx context.Context) ([]InviteView, error)
	CancelInvite(ctx context.Context, id string) error
	ListDevices(ctx context.Context) ([]DeviceView, error)
	RenameDevice(ctx context.Context, id, label string) error
	RevokeDevice(ctx context.Context, id, by string) error
	ListRelays(ctx context.Context) ([]Relay, error)
	AddRelay(ctx context.Context, addr, label, claimCode string) (Relay, error)
	RemoveRelay(ctx context.Context, id string) error
	CheckRelay(ctx context.Context, id string) (RelayCheck, error)
	SyncRelay(ctx context.Context, id string) (Relay, error)
	ClaimRelay(ctx context.Context, id, code string) (Relay, error)
	RelayLimits(ctx context.Context, id string) (RelayLimitsInfo, error)
	SetRelayLimits(ctx context.Context, id string, req SetRelayLimitsRequest) (RelayLimitsInfo, error)
	RegenerateIdentity(ctx context.Context) (Status, error)
	// Redeem is served over P2P only; from is the caller's peer ID as the
	// connection authenticated it.
	Redeem(ctx context.Context, from string, req RedeemRequest) (RedeemResult, error)
}

// Status is the state of the P2P layer.
type Status struct {
	// Mode is the mode in effect: off, connect or share.
	Mode string `json:"mode"`
	// Enabled is true when the mode is not off.
	Enabled bool `json:"enabled"`
	// Running is true when the host runs.
	Running bool `json:"running"`
	// Error says why the host does not run although the mode asks for it.
	Error string `json:"error,omitempty"`
	// PeerID is the peer ID of this server; empty when the host does not run.
	PeerID string `json:"peer_id,omitempty"`
	// Addrs are the addresses of the host, each with /p2p/<peer ID>.
	Addrs []string `json:"addrs"`
	// Reachability is unknown, public or private.
	Reachability string        `json:"reachability"`
	PublicDHT    PublicDHTInfo `json:"public_dht"`
	// Reservations are the peer IDs of the relays that hold a reservation for
	// this server.
	Reservations []string `json:"reservations"`
	// RelayService says whether this server relays for its paired devices.
	RelayService RelayServiceInfo `json:"relay_service"`
	// PendingInvites is the number of invites that can still be redeemed.
	PendingInvites int `json:"pending_invites"`
	// Devices is the number of active paired devices.
	Devices int `json:"devices"`
}

// PublicDHTInfo is the state of the public DHT.
type PublicDHTInfo struct {
	On bool `json:"on"`
	// Peers is the number of peers the host is connected to.
	Peers int `json:"peers"`
}

// RelayServiceInfo is the state of the in-process relay ("Via your server").
type RelayServiceInfo struct {
	Running bool `json:"running"`
	// Reason says why it does not run: off, not_share, not_public.
	Reason string `json:"reason,omitempty"`
}

// MintRequest is the body of POST /api/p2p/invites.
type MintRequest struct {
	// Kind is device, remote, worker or split.
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// TTLSeconds is the life of the invite: 600 when zero, at most 86400.
	TTLSeconds int `json:"ttl_seconds"`
	// Models limits a remote invite to these models. An empty list means
	// every model, as before all_models existed.
	Models []string `json:"models,omitempty"`
	// AllModels grants a remote invite every model this server lets a client
	// key use, models added later too. It is refused with a non-empty models.
	AllModels bool              `json:"all_models,omitempty"`
	Features  []string          `json:"features,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	// Relays is "auto", "none" or a list of relay IDs.
	Relays RelayChoice `json:"relays" swaggertype:"string" example:"auto"`
	// AllowOpenRelay lets the invite name an open relay.
	AllowOpenRelay bool `json:"allow_open_relay"`
}

// RelayChoice is "auto", "none" or a list of relay IDs.
type RelayChoice struct {
	// Mode is auto, none or explicit.
	Mode string
	// IDs are the relays of mode explicit.
	IDs []string
}

// UnmarshalJSON reads "auto", "none", null or a list of IDs.
func (r *RelayChoice) UnmarshalJSON(b []byte) error {
	var s *string
	if err := json.Unmarshal(b, &s); err == nil {
		switch {
		case s == nil || *s == "" || *s == "auto":
			*r = RelayChoice{Mode: "auto"}
		case *s == "none":
			*r = RelayChoice{Mode: "none"}
		default:
			return fmt.Errorf("relays: use \"auto\", \"none\" or a list of relay IDs")
		}
		return nil
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return fmt.Errorf("relays: use \"auto\", \"none\" or a list of relay IDs")
	}
	*r = RelayChoice{Mode: "explicit", IDs: ids}
	return nil
}

// MarshalJSON writes the choice in the form it is read.
func (r RelayChoice) MarshalJSON() ([]byte, error) {
	if r.Mode == "explicit" {
		return json.Marshal(r.IDs)
	}
	if r.Mode == "" {
		return json.Marshal("auto")
	}
	return json.Marshal(r.Mode)
}

// MintResult is the answer of POST /api/p2p/invites.
type MintResult struct {
	ID        string    `json:"id"`
	Link      string    `json:"link"`
	Kind      string    `json:"kind"`
	ExpiresAt time.Time `json:"expires_at"`
	// QR says whether the interface may show the link as a QR code (device
	// and remote invites only).
	QR       bool      `json:"qr"`
	Relay    RelayPass `json:"relay"`
	Warnings []string  `json:"warnings"`
}

// RelayPass says which relay holds the pass of an invite, or why none does.
type RelayPass struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
	// Pass is true when the link carries a relay pass.
	Pass bool `json:"pass"`
	Open bool `json:"open"`
	// Reason is no_relay or relay_unreachable when there is no pass.
	Reason string `json:"reason,omitempty"`
}

// InviteView is one invite in GET /api/p2p/invites.
type InviteView struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	ExpiresAt time.Time `json:"expires_at"`
	// Status is pending, used, expired or canceled.
	Status   string `json:"status"`
	DeviceID string `json:"device_id,omitempty"`
	// DevicePeerID is the peer that redeemed a used invite.
	DevicePeerID string `json:"device_peer_id,omitempty"`
	// Phrase is the confirmation phrase of a used invite: the words the
	// device was shown when it redeemed. The admin compares them.
	Phrase string `json:"phrase,omitempty"`
	// RelayPass is the label of the relay that holds the pass of the invite;
	// empty when the link carries no pass.
	RelayPass string `json:"relay_pass,omitempty"`
	// Models are the models a remote invite is limited to. Device and worker
	// invites carry none.
	Models []string `json:"models,omitempty"`
	// AllModels is true for a remote invite that grants every model, also
	// for one made without a list.
	AllModels bool `json:"all_models,omitempty"`
}

// DeviceView is one device in GET /api/p2p/devices.
type DeviceView struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Label      string     `json:"label"`
	PeerID     string     `json:"peer_id"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// Status is active or revoked.
	Status string `json:"status"`
	// Online is true while the device is connected.
	Online bool     `json:"online"`
	Path   PathView `json:"path"`
	// Models are the models a remote device is limited to.
	Models []string `json:"models,omitempty"`
	// AllModels is true for a remote device that may use every model.
	AllModels bool `json:"all_models,omitempty"`
}

// PathView says how a device reaches the server.
type PathView struct {
	// Kind is none, direct, hole_punched or relayed.
	Kind         string `json:"kind"`
	RelayLabel   string `json:"relay_label,omitempty"`
	Limited      bool   `json:"limited"`
	LimitBytes   int64  `json:"limit_bytes,omitempty"`
	LimitSeconds int64  `json:"limit_seconds,omitempty"`
	RTTMillis    int64  `json:"rtt_ms,omitempty"`
}

// Relay is a relay the server knows.
type Relay struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Addr is a multiaddr that ends in /p2p/<relay peer ID>.
	Addr string `json:"addr"`
	// Mode is gated, open, unclaimed or unknown.
	Mode string `json:"mode"`
	// State is in_sync, out_of_sync, open, unclaimed, not_admin, denied,
	// unreachable or checking.
	State     string      `json:"state"`
	Health    string      `json:"health,omitempty"`
	LatencyMS int64       `json:"latency_ms,omitempty"`
	Admins    int         `json:"admins"`
	Members   int         `json:"members"`
	Limits    RelayLimits `json:"limits"`
	CheckedAt *time.Time  `json:"checked_at,omitempty"`
}

// RelayLimits are the limits a relay reports.
type RelayLimits struct {
	CircuitBytes   int64 `json:"circuit_bytes"`
	CircuitSeconds int64 `json:"circuit_seconds"`
	RateBPS        int64 `json:"rate_bps"`
}

// RelayLimitsInfo is the answer of GET and PUT /api/p2p/relays/{id}/limits.
type RelayLimitsInfo struct {
	Limits RelayLimits `json:"limits"`
	// Ceiling is the most a change may set for each limit; the operator of
	// the relay sets it. Zero means no ceiling. With a ceiling a limit
	// cannot be removed either: a value above it, or zero, is refused with
	// an error that names the ceiling.
	Ceiling RelayLimits `json:"ceiling"`
	// CanChange is true when this server is an admin of a gated relay and
	// may change the limits.
	CanChange bool `json:"can_change"`
	// Restarted is true when the relay restarted its circuit service to
	// apply the change: it dropped its reservations and the paired devices
	// reserve again within moments. A change of the bandwidth limit alone
	// never restarts it.
	Restarted bool `json:"restarted,omitempty"`
}

// SetRelayLimitsRequest is the body of PUT /api/p2p/relays/{id}/limits. A
// field that is left out stays as it is; zero removes that limit.
type SetRelayLimitsRequest struct {
	// CircuitBytes is the data one circuit may carry in each direction.
	CircuitBytes *int64 `json:"circuit_bytes,omitempty"`
	// CircuitSeconds is the time one circuit may stay open.
	CircuitSeconds *int64 `json:"circuit_seconds,omitempty"`
	// RateBPS is the bandwidth of one circuit in each direction, in bytes
	// per second.
	RateBPS *int64 `json:"rate_bps,omitempty"`
}

// RelayCheck is the result of POST /api/p2p/relays/{id}/check.
type RelayCheck struct {
	Connected bool     `json:"connected"`
	Reserved  bool     `json:"reserved"`
	AdminOK   bool     `json:"admin_ok"`
	CircuitOK bool     `json:"circuit_ok"`
	InSync    bool     `json:"in_sync"`
	Detail    []string `json:"detail"`
	Relay     Relay    `json:"relay"`
}

// AddRelayRequest is the body of POST /api/p2p/relays.
type AddRelayRequest struct {
	// Addr must end in /p2p/<relay peer ID>.
	Addr      string `json:"addr"`
	Label     string `json:"label"`
	ClaimCode string `json:"claim_code,omitempty"`
}

// ClaimRelayRequest is the body of POST /api/p2p/relays/{id}/claim.
type ClaimRelayRequest struct {
	Code string `json:"code"`
}

// RenameDeviceRequest is the body of PATCH /api/p2p/devices/{id}.
type RenameDeviceRequest struct {
	Label string `json:"label"`
}

// RegenerateIdentityRequest is the body of POST /api/p2p/identity/regenerate.
type RegenerateIdentityRequest struct {
	// Confirm must be true: a new identity unpairs every device and remote.
	Confirm bool `json:"confirm"`
}

// RedeemRequest is the body of POST /api/p2p/redeem.
type RedeemRequest struct {
	// V is the protocol version, 1.
	V        int    `json:"v"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// RedeemResult is the answer of a redeem.
type RedeemResult struct {
	DeviceID string `json:"device_id"`
	Kind     string `json:"kind"`
	// Phrase is shown on both sides; the person compares them.
	Phrase string     `json:"phrase"`
	Server ServerInfo `json:"server"`
	// ClientKey is the API key of a remote invite; it is shown once.
	ClientKey *ClientKey `json:"client_key,omitempty"`
}

// ServerInfo names the server that answered a redeem.
type ServerInfo struct {
	PeerID  string `json:"peer_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientKey is the client key of a remote invite.
type ClientKey struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

// RevokeResult is the answer of DELETE /api/p2p/devices/{id}.
type RevokeResult struct {
	Revoked bool `json:"revoked"`
}

// Errors a Service returns. The handlers map them to status codes.
var (
	// ErrNotFound: 404.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: 400. Wrap it with the reason.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict: 409. Wrap it with the reason.
	ErrConflict = errors.New("conflict")
	// ErrInviteInvalid: 410, the one answer for a wrong, used, canceled or
	// expired code.
	ErrInviteInvalid = errors.New("the invite is not valid")
	// ErrRateLimited: 429.
	ErrRateLimited = errors.New("too many attempts, try again later")
	// ErrUnavailable: 503. Wrap it with the reason.
	ErrUnavailable = errors.New("unavailable")
	// ErrNeedsAccount: 409. The key of a remote belongs to the user account
	// that shares, and the caller has none: it signed in with the API key of
	// LOCALAI_API_KEY.
	ErrNeedsAccount = errors.New("sharing with a remote needs a signed-in user account, because the remote gets an API key of that account. The API key set with LOCALAI_API_KEY has no account: sign in with a user account and create the invite again")
)

// ModeError says that the operation needs another P2P mode. The handlers
// answer 409 with its text.
type ModeError struct {
	// Mode is the mode in effect.
	Mode string
	// Need is the mode the operation needs.
	Need string
}

func (e *ModeError) Error() string {
	if e.Need == "share" {
		return fmt.Sprintf("P2P mode is %q: this needs mode \"share\" (Connect and share), which accepts paired devices", e.Mode)
	}
	return fmt.Sprintf("P2P mode is %q: this needs P2P to be on (mode \"connect\" or \"share\")", e.Mode)
}

type peerKey struct{}

// WithPeer marks a request context as arriving over P2P from peer.
func WithPeer(ctx context.Context, peer string) context.Context {
	return context.WithValue(ctx, peerKey{}, peer)
}

// PeerFrom returns the peer of a request that arrived over P2P.
func PeerFrom(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(peerKey{}).(string)
	return p, ok && p != ""
}
