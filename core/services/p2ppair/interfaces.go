package p2ppair

import (
	"context"
	"time"

	ma "github.com/multiformats/go-multiaddr"
)

// Relay is a relay that the server knows and that an invite may name.
type Relay struct {
	// ID is the server's own identifier of the relay.
	ID    string
	Label string
	// Addr is a transport address that ends in /p2p/<relay peer ID>.
	Addr ma.Multiaddr
	// Open is true for a relay that anyone may use. Open relays are named in
	// an invite only when the administrator allowed it, and the link marks
	// them.
	Open bool
}

// RelaySource lists the relays the server knows, in order of preference.
type RelaySource interface {
	Relays(ctx context.Context) []Relay
}

// PassRequest asks a relay to admit one unknown peer that presents the pass.
type PassRequest struct {
	Relay    Relay
	InviteID string
	Code     string
	TTL      time.Duration
	Label    string
}

// PassRegistrar registers and cancels relay passes. The caller implements it
// on top of the relay admin protocol.
type PassRegistrar interface {
	// RegisterPass registers a pass on the relay and returns the pass to put
	// in the link.
	RegisterPass(ctx context.Context, req PassRequest) (pass string, err error)
	// CancelPass drops the pass of an invite on the relay.
	CancelPass(ctx context.Context, relay Relay, inviteID string) error
}

// IssuedKey is a client key issued for a remote invite.
type IssuedKey struct {
	// ID identifies the key row; it is kept on the device.
	ID string `json:"id"`
	// Secret is the plaintext key. It is shown once.
	Secret string `json:"secret"`
}

// ClientKeyIssuer creates the API key of a remote invite. The caller wires it
// to the client pairing flow of the auth package.
type ClientKeyIssuer interface {
	// Issue creates a key for the invite's owner and scope. It runs inside
	// the redeem transaction: with the GORM store tx.DB is set and Issue
	// should use it, so a failure leaves nothing behind. With the JSON store
	// tx.DB is nil and the redeem is undone by not being written.
	Issue(ctx context.Context, tx Tx, inv Invite, dev Device) (IssuedKey, error)
	// Revoke revokes the key of a revoked device.
	Revoke(ctx context.Context, keyID string) error
}
