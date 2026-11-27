package p2ppair

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"gorm.io/gorm"
)

// Tx is the transaction that a store redeem runs in. DB is set for the GORM
// store and nil for the JSON store, which has no transactions but applies the
// redeem as a whole or not at all.
type Tx struct{ DB *gorm.DB }

// IssueFunc runs inside a redeem, after the invite is claimed and before the
// device is final. It returns the ID of the client key it issued. If it
// returns an error, the redeem is undone: the invite stays unused and no
// device exists.
type IssueFunc func(ctx context.Context, tx Tx, inv Invite, dev Device) (clientKeyID string, err error)

// RedeemParams is what a store needs to redeem a code.
type RedeemParams struct {
	// Code is the plaintext code as the caller sent it.
	Code string
	// Peer is the authenticated peer ID of the caller.
	Peer peer.ID
	// Label names the new device.
	Label string
	// Now is the time of the redeem.
	Now time.Time
	// DeviceID is the ID of the device row to create.
	DeviceID string
	// Issue is optional; see IssueFunc.
	Issue IssueFunc
	// CredentialHash is the keyed hash of the sign-in credential of the new
	// device. It is written with the device row, so a failed redeem leaves
	// no credential behind. The store keeps it only when the invite has
	// SignIn; a device of any other invite gets no credential.
	CredentialHash string
	// MaxDevicesForOwner limits the active sign-in devices of the owner of a
	// sign-in invite; zero means no limit. The store counts them in the
	// redeem transaction and returns ErrDeviceLimit when the owner already
	// has that many; the invite then stays unused. An invite without SignIn
	// is not limited, and devices without SignIn do not count.
	MaxDevicesForOwner int
}

// Store keeps invites and devices. Both implementations pass the same tests.
type Store interface {
	// CreateInvite stores inv and the hash of code. The ID must be unique.
	CreateInvite(ctx context.Context, inv Invite, code string) error
	// GetInvite returns one invite or ErrNotFound.
	GetInvite(ctx context.Context, id string) (Invite, error)
	// ListInvites returns all invites, newest first.
	ListInvites(ctx context.Context) ([]Invite, error)
	// ListInvitesByOwner returns the invites of one owner, newest first.
	ListInvitesByOwner(ctx context.Context, ownerID string) ([]Invite, error)
	// CountPendingInvitesByOwner counts the invites of one owner that are
	// pending at now.
	CountPendingInvitesByOwner(ctx context.Context, ownerID string, now time.Time) (int, error)
	// CancelInvite cancels a pending invite. It returns ErrNotFound for an
	// unknown ID and ErrNotPending for an invite that was used, canceled or
	// has expired.
	CancelInvite(ctx context.Context, id string, now time.Time) error

	// Redeem claims the invite for the code (single use, compare and set on
	// used_at) and creates the device. Every failure to claim returns
	// ErrInviteInvalid, whatever the reason. A failure of the store itself,
	// such as a database error, is returned as it is, wrapped, and is never
	// ErrInviteInvalid. A peer that already has an active
	// device gets ErrPeerPaired and the invite stays unused.
	Redeem(ctx context.Context, p RedeemParams) (Invite, Device, error)

	// GetDevice returns one device or ErrNotFound.
	GetDevice(ctx context.Context, id string) (Device, error)
	// GetActiveDeviceByPeer returns the device of peer p that is not
	// revoked, or ErrNotFound. A peer has at most one.
	GetActiveDeviceByPeer(ctx context.Context, p peer.ID) (Device, error)
	// ListDevices returns all devices, revoked ones included, newest first.
	ListDevices(ctx context.Context) ([]Device, error)
	// ListDevicesByOwner returns the devices of one owner, revoked ones
	// included, newest first.
	ListDevicesByOwner(ctx context.Context, ownerID string) ([]Device, error)
	// CountActiveDevicesByOwner counts the devices of one owner that are not
	// revoked.
	CountActiveDevicesByOwner(ctx context.Context, ownerID string) (int, error)
	// SetDeviceCredential sets the keyed hash of the sign-in credential of a
	// device; an empty hash clears it. It returns ErrNotFound for an unknown
	// ID.
	SetDeviceCredential(ctx context.Context, id, hash string) error
	// RenameDevice sets the label of a device.
	RenameDevice(ctx context.Context, id, label string) error
	// SetDeviceAllowSlowPath sets whether the server may use the device
	// over a relayed path. It returns ErrNotFound for an unknown ID.
	SetDeviceAllowSlowPath(ctx context.Context, id string, allow bool) error
	// SetDeviceNode attaches a split device to a backend node, or to the
	// server with an empty node, and stores note as the reason (empty clears
	// it). It returns ErrNotFound for an unknown ID.
	SetDeviceNode(ctx context.Context, id, node, note string) error
	// RevokeDevice sets revoked_at and revoked_by and clears the credential
	// hash, in one update. Revoking a revoked device
	// changes nothing and succeeds. It returns the device as it is stored.
	RevokeDevice(ctx context.Context, id, by string, now time.Time) (Device, error)
	// TouchDevice records when and by which path an active device was seen.
	// An unknown peer is not an error.
	TouchDevice(ctx context.Context, p peer.ID, at time.Time, path string) error
}

// codeMatches compares two hashes without leaking where they differ.
func codeMatches(a, b string) bool {
	return len(a) == len(b) && constantTimeEq(a, b)
}
