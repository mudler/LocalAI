package p2ppair

import (
	"errors"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// Kind says what an invite or a device is for.
type Kind string

// The kinds of invite and device.
const (
	// KindDevice admits a person's device to the peer-to-peer network. It
	// issues no key: the person signs in as usual.
	KindDevice Kind = "device"
	// KindRemote admits a client that gets a scoped API key.
	KindRemote Kind = "remote"
	// KindWorker admits a worker node.
	KindWorker Kind = "worker"
	// KindSplit admits a node that holds a part of a split model.
	KindSplit Kind = "split"
)

// Valid reports whether k is one of the known kinds.
func (k Kind) Valid() bool {
	switch k {
	case KindDevice, KindRemote, KindWorker, KindSplit:
		return true
	}
	return false
}

// Scope limits what the holder of an invite or device may use.
type Scope struct {
	// Models are the models a remote may use. With AllModels false and no
	// list, a remote gets every model too (the rule before AllModels existed).
	Models []string `json:"models,omitempty"`
	// AllModels says that a remote may use every model, explicitly. It never
	// goes with a list of models.
	AllModels bool              `json:"all_models,omitempty"`
	Features  []string          `json:"features,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// InviteStatus is the state of an invite at one moment.
type InviteStatus string

// The states of an invite.
const (
	InvitePending  InviteStatus = "pending"
	InviteUsed     InviteStatus = "used"
	InviteExpired  InviteStatus = "expired"
	InviteCanceled InviteStatus = "canceled"
)

// Invite is a one-time pairing code with its settings. The code itself is
// never stored; only a keyed hash of it is.
type Invite struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
	Kind    Kind   `json:"kind"`
	Label   string `json:"label"`
	Scope   Scope  `json:"scope"`
	// RelayIDs are the relays named in the link.
	RelayIDs []string `json:"relay_ids,omitempty"`
	// PassRelayID is the relay where a pass for this invite was registered;
	// empty when no pass was registered.
	PassRelayID string `json:"pass_relay_id,omitempty"`
	// OmitLANAddrs is the choice of the invite to keep the local network
	// addresses of the server out of the link. The device that redeems the
	// invite keeps it, so that its routes leave them out too.
	OmitLANAddrs bool       `json:"omit_lan_addrs,omitempty"`
	ExpiresAt    time.Time  `json:"expires_at"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	UsedByPeer   string     `json:"used_by_peer,omitempty"`
	DeviceID     string     `json:"device_id,omitempty"`
	CreatedBy    string     `json:"created_by"`
	CanceledAt   *time.Time `json:"canceled_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	// SignIn marks a device invite whose device gets a credential at redeem
	// time, to sign in as the owner. The kind stays KindDevice, so that an
	// app that does not know the flag still pairs the device.
	SignIn bool `json:"sign_in,omitempty"`
}

// Status returns the state of the invite at now.
func (i Invite) Status(now time.Time) InviteStatus {
	switch {
	case i.UsedAt != nil:
		return InviteUsed
	case i.CanceledAt != nil:
		return InviteCanceled
	case !now.Before(i.ExpiresAt):
		return InviteExpired
	}
	return InvitePending
}

// DeviceStatus is the state of a device.
type DeviceStatus string

// The states of a device.
const (
	DeviceActive  DeviceStatus = "active"
	DeviceRevoked DeviceStatus = "revoked"
)

// Device is a peer that redeemed an invite.
type Device struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
	Kind    Kind   `json:"kind"`
	Label   string `json:"label"`
	// PeerID is the libp2p peer ID. At most one active device has a given
	// peer ID.
	PeerID string `json:"peer_id"`
	// ClientKeyID is the ID of the API key issued at redeem time (remote
	// kind only).
	ClientKeyID string     `json:"client_key_id,omitempty"`
	InviteID    string     `json:"invite_id"`
	Scope       Scope      `json:"scope"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	// LastPath describes how the device last reached the server, for example
	// "direct", "hole-punched" or "relay".
	LastPath  string     `json:"last_path,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	RevokedBy string     `json:"revoked_by,omitempty"`
	// AllowSlowPath is set when the administrator chose to use a split
	// worker that the server reaches only through a relay ("Use anyway").
	AllowSlowPath bool `json:"allow_slow_path,omitempty"`
	// NodeID is the backend node of a cluster that a split device lends its
	// memory to; empty means the server that paired it. A split invite may
	// name the first node in its NodeLabel label.
	NodeID string `json:"node_id,omitempty"`
	// NodeNote says why a split device is attached to no node any more, for
	// example because its node stopped being a split server. Attaching the
	// device clears it.
	NodeNote string `json:"node_note,omitempty"`
	// OmitLANAddrs is OmitLANAddrs of the invite of the device: the routes
	// of the device leave out the local network addresses of the server.
	OmitLANAddrs bool `json:"omit_lan_addrs,omitempty"`
	// SignIn is SignIn of the invite of the device: the device holds a
	// credential to sign in as its owner.
	SignIn bool `json:"sign_in,omitempty"`
	// CredentialHash is the keyed hash of the sign-in credential of the
	// device; empty when it has none or was revoked. It is never part of a
	// JSON view.
	CredentialHash string `json:"-"`
}

// NodeLabel is the scope label of a split invite that names the backend node
// the device is attached to when it redeems.
const NodeLabel = "node"

// Status returns whether the device is active.
func (d Device) Status() DeviceStatus {
	if d.RevokedAt != nil {
		return DeviceRevoked
	}
	return DeviceActive
}

// Errors returned by the package. The redeem path returns ErrInviteInvalid
// for a wrong, used, canceled and expired code alike, so that a caller learns
// nothing about which codes exist.
var (
	// ErrInviteInvalid is the one error for a code that cannot be redeemed.
	ErrInviteInvalid = errors.New("p2ppair: the invite is not valid")
	// ErrRateLimited means too many wrong codes were sent.
	ErrRateLimited = errors.New("p2ppair: too many attempts")
	// ErrMalformed means the request is not well formed.
	ErrMalformed = errors.New("p2ppair: malformed request")
	// ErrPeerPaired means the peer already has an active device.
	ErrPeerPaired = errors.New("p2ppair: the peer is already paired")
	// ErrNotFound means the invite or device does not exist.
	ErrNotFound = errors.New("p2ppair: not found")
	// ErrNotPending means the invite is no longer pending.
	ErrNotPending = errors.New("p2ppair: the invite is not pending")
	// ErrNoKeyIssuer means a remote invite was asked for with no key issuer.
	ErrNoKeyIssuer = errors.New("p2ppair: no client key issuer is configured")
	// ErrDeviceLimit means the owner of a sign-in invite already has as many
	// active sign-in devices as allowed.
	ErrDeviceLimit = errors.New("p2ppair: the owner has too many devices")
	// ErrCredentialInvalid is the one error for a device credential that is
	// wrong, or whose device is unknown, revoked or has no sign-in.
	ErrCredentialInvalid = errors.New("p2ppair: the device credential is not valid")
)

// parsePeer validates a peer ID string.
func parsePeer(s string) (peer.ID, error) {
	id, err := peer.Decode(s)
	if err != nil {
		return "", err
	}
	return id, nil
}
