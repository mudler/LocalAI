package config

import (
	"fmt"
	"strings"
)

// P2PMode says what the P2P layer of a server does.
type P2PMode string

const (
	// P2PModeOff runs no P2P layer. It is the default.
	P2PModeOff P2PMode = "off"
	// P2PModeConnect lets this server dial out to remotes over P2P. It accepts
	// nobody: there is no redeem or HTTP listener, no invites and no sharing.
	// It needs no authentication.
	P2PModeConnect P2PMode = "connect"
	// P2PModeShare is connect plus accepting paired devices: the listener,
	// invites, sharing this instance, and the devices and relays admin.
	// Anyone who pairs gets an identity, so it requires authentication.
	P2PModeShare P2PMode = "share"
)

// ErrP2PShareNeedsAuth is the reason P2P mode share is refused while
// authentication is off. The startup check and the runtime setting show the
// same text.
const ErrP2PShareNeedsAuth = "P2P mode \"share\" needs authentication: it accepts paired devices, and without accounts there is no way to tell them apart. " +
	"Turn authentication on (--auth-enabled or LOCALAI_AUTH), or use P2P mode \"connect\", which only dials out and needs no authentication"

// ResolveP2PMode checks the requested P2P mode against the authentication
// state and returns the mode that may run. An empty request means off. The
// message is a short human line about the effective mode; it is empty for off.
// Requesting share while authentication is off is an error carrying
// ErrP2PShareNeedsAuth.
func ResolveP2PMode(requested string, authEnabled bool) (P2PMode, string, error) {
	switch P2PMode(strings.ToLower(strings.TrimSpace(requested))) {
	case "", P2PModeOff:
		return P2PModeOff, "", nil
	case P2PModeConnect:
		return P2PModeConnect, "P2P mode \"connect\": this server dials out to remotes over P2P and accepts no one, so invites and sharing are unavailable. Use mode \"share\" (needs authentication) to accept paired devices", nil
	case P2PModeShare:
		if !authEnabled {
			return P2PModeOff, ErrP2PShareNeedsAuth, fmt.Errorf("%s", ErrP2PShareNeedsAuth)
		}
		return P2PModeShare, "P2P mode \"share\": this server dials out and accepts paired devices", nil
	default:
		msg := fmt.Sprintf("unknown P2P mode %q: use off, connect or share", requested)
		return P2PModeOff, msg, fmt.Errorf("%s", msg)
	}
}

// Accepts reports whether the mode accepts paired devices.
func (m P2PMode) Accepts() bool { return m == P2PModeShare }

// Dials reports whether the mode may dial out to remotes.
func (m P2PMode) Dials() bool { return m == P2PModeConnect || m == P2PModeShare }
