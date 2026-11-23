package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// P2PRelayMode says when the server relays for its paired devices.
type P2PRelayMode string

const (
	// P2PRelayAuto relays while the host is publicly reachable. It is the
	// default.
	P2PRelayAuto P2PRelayMode = "auto"
	// P2PRelayOn relays whatever the reachability.
	P2PRelayOn P2PRelayMode = "on"
	// P2PRelayOff never relays.
	P2PRelayOff P2PRelayMode = "off"
)

// P2PRelay is a relay the server knows, as stored in the p2p_relays setting.
type P2PRelay struct {
	ID    string `json:"id"`
	Addr  string `json:"addr"`
	Label string `json:"label,omitempty"`
}

// P2PSettings is the configuration of the P2P layer. The zero value is the
// default: mode off, public DHT on, mDNS on, relaying for paired devices on in
// mode auto, no rate limit. The "No" and "Disable" fields are negations so
// that the zero value keeps the defaults.
type P2PSettings struct {
	// Mode is off, connect or share; empty means off.
	Mode string
	// DisablePublicDHT turns off the public DHT (setting p2p_public_dht).
	DisablePublicDHT bool
	// BootstrapPeers are multiaddrs with /p2p/; empty uses the public
	// bootstrap peers when the public DHT is on.
	BootstrapPeers []string
	// Relays are the relays the server knows.
	Relays []P2PRelay
	// NoRelayPaired turns off relaying for paired devices (setting
	// p2p_relay_paired).
	NoRelayPaired bool
	// RelayMode is auto, on or off; empty means auto.
	RelayMode string
	// RelayRateLimit is the bandwidth limit of the in-process relay in bytes
	// per second per circuit; 0 means no limit.
	RelayRateLimit int64
	// DisableMDNS turns off local network discovery (setting p2p_mdns).
	DisableMDNS bool
	// ListenAddrs are the listen multiaddrs of mode share; empty uses the
	// defaults of the host. Startup only (--p2p-listen).
	ListenAddrs []string
	// IdentityPath is the key file of the peer identity. Empty means
	// <data path>/p2p/identity.key. Startup only (--p2p-identity).
	IdentityPath string
}

// EffectiveMode is the requested mode with an empty value read as off. It
// does not check authentication; see ResolveP2PMode.
func (s P2PSettings) EffectiveMode() P2PMode {
	m := P2PMode(strings.ToLower(strings.TrimSpace(s.Mode)))
	if m == "" {
		return P2PModeOff
	}
	return m
}

// EffectiveRelayMode is the relay mode with an empty value read as auto.
func (s P2PSettings) EffectiveRelayMode() P2PRelayMode {
	m := P2PRelayMode(strings.ToLower(strings.TrimSpace(s.RelayMode)))
	if m == "" {
		return P2PRelayAuto
	}
	return m
}

// ErrP2PNoDataPath is the reason P2P does not start without a data path.
const ErrP2PNoDataPath = "P2P needs a data path: the peer identity of this server is a key file that must survive a restart, " +
	"or every paired device and remote would lose this server. Set --data-path (not \"-\"), or name the key file with --p2p-identity"

// ResolveP2PIdentityPath returns the key file of the peer identity: the
// explicit path, else <dataPath>/p2p/identity.key. With neither it returns
// an error carrying ErrP2PNoDataPath.
func ResolveP2PIdentityPath(explicit, dataPath string) (string, error) {
	switch {
	case explicit != "":
		return explicit, nil
	case dataPath != "":
		return filepath.Join(dataPath, "p2p", "identity.key"), nil
	}
	return "", errors.New(ErrP2PNoDataPath)
}

// ValidateP2PMode checks a p2p_mode value.
func ValidateP2PMode(v string) error {
	switch P2PMode(strings.ToLower(strings.TrimSpace(v))) {
	case "", P2PModeOff, P2PModeConnect, P2PModeShare:
		return nil
	}
	return fmt.Errorf("unknown P2P mode %q: use off, connect or share", v)
}

// ValidateP2PRelayMode checks a p2p_relay_mode value.
func ValidateP2PRelayMode(v string) error {
	switch P2PRelayMode(strings.ToLower(strings.TrimSpace(v))) {
	case "", P2PRelayAuto, P2PRelayOn, P2PRelayOff:
		return nil
	}
	return fmt.Errorf("unknown P2P relay mode %q: use auto, on or off", v)
}

// ValidateP2PPeerAddrs checks a list of multiaddrs that must each name a peer
// with /p2p/<peer ID>, as bootstrap peers and relays do.
func ValidateP2PPeerAddrs(addrs []string) error {
	for _, a := range addrs {
		if _, err := p2pnet.ParsePeerAddr(a); err != nil {
			return err
		}
	}
	return nil
}

// ValidateP2PRelays checks the p2p_relays setting: each relay has a unique
// ID and an address that names its peer.
func ValidateP2PRelays(relays []P2PRelay) error {
	seen := map[string]bool{}
	for _, r := range relays {
		if strings.TrimSpace(r.ID) == "" {
			return errors.New("a relay needs an id")
		}
		if seen[r.ID] {
			return fmt.Errorf("relay id %q is used twice", r.ID)
		}
		seen[r.ID] = true
		if _, err := p2pnet.ParsePeerAddr(r.Addr); err != nil {
			return err
		}
	}
	return nil
}

// P2PRelaysEqual compares two relay lists in order.
func P2PRelaysEqual(a, b []P2PRelay) bool { return slices.Equal(a, b) }

// WithP2PMode sets the requested P2P mode (--p2p-mode).
func WithP2PMode(mode string) AppOption {
	return func(o *ApplicationConfig) { o.P2P.Mode = mode }
}

// WithP2PListenAddrs sets the listen multiaddrs of mode share (--p2p-listen).
func WithP2PListenAddrs(addrs []string) AppOption {
	return func(o *ApplicationConfig) { o.P2P.ListenAddrs = addrs }
}

// WithP2PBootstrapPeers sets the bootstrap peers (--p2p-bootstrap).
func WithP2PBootstrapPeers(addrs []string) AppOption {
	return func(o *ApplicationConfig) { o.P2P.BootstrapPeers = addrs }
}

// WithP2PIdentityPath sets the key file of the peer identity (--p2p-identity).
func WithP2PIdentityPath(path string) AppOption {
	return func(o *ApplicationConfig) { o.P2P.IdentityPath = path }
}
