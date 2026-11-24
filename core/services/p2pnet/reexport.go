package p2pnet

import (
	"fmt"
	"net"
	"strings"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// The types below let the rest of LocalAI name peers, addresses and
// reachability without importing libp2p. They are aliases, so values pass
// between p2pnet and its callers unchanged.

// ID is a peer ID.
type ID = peer.ID

// AddrInfo is a peer ID with its addresses.
type AddrInfo = peer.AddrInfo

// Protocol is the ID of a stream protocol.
type Protocol = protocol.ID

// Multiaddr is a multiaddr.
type Multiaddr = ma.Multiaddr

// Reachability is what AutoNAT found out about a host.
type Reachability = network.Reachability

// The reachability values.
const (
	ReachabilityUnknown = network.ReachabilityUnknown
	ReachabilityPublic  = network.ReachabilityPublic
	ReachabilityPrivate = network.ReachabilityPrivate
)

// Subscription is a subscription to host events. Its Out channel carries the
// events and Close ends it.
type Subscription = event.Subscription

// The multiaddr protocol codes that callers look for.
const (
	ProtocolP2P     = ma.P_P2P
	ProtocolCircuit = ma.P_CIRCUIT
	// ProtocolTCP is the multiaddr protocol code of TCP.
	ProtocolTCP = ma.P_TCP
)

// DecodePeerID parses the text form of a peer ID. It is like ParsePeerID
// without the error prefix.
func DecodePeerID(s string) (ID, error) { return peer.Decode(s) }

// ParsePeerAddr parses a multiaddr that ends in /p2p/<peer ID>. It trims
// white space around the address.
func ParsePeerAddr(a string) (*AddrInfo, error) {
	m, err := ma.NewMultiaddr(strings.TrimSpace(a))
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", a, err)
	}
	ai, err := peer.AddrInfoFromP2pAddr(m)
	if err != nil {
		return nil, fmt.Errorf("address %q must end in /p2p/<peer ID>: %w", a, err)
	}
	return ai, nil
}

// ParsePeerAddrs parses a list of multiaddrs that end in /p2p/<peer ID>, with
// ParsePeerAddr, and stops at the first one that does not parse.
func ParsePeerAddrs(addrs []string) ([]AddrInfo, error) {
	out := make([]AddrInfo, 0, len(addrs))
	for _, a := range addrs {
		ai, err := ParsePeerAddr(a)
		if err != nil {
			return nil, err
		}
		out = append(out, *ai)
	}
	return out, nil
}

// IsPublicAddr says whether a is a public IP address.
func IsPublicAddr(a Multiaddr) bool { return manet.IsPublicAddr(a) }

// IsLANAddr says whether a is an address of a local network: a private IPv4
// address (RFC 1918) or a unique local IPv6 address (fc00::/7). Loopback,
// link-local and unspecified addresses are not LAN addresses: a peer on
// another machine cannot use them.
func IsLANAddr(a Multiaddr) bool {
	ip, err := manet.ToIP(a)
	if err != nil {
		return false
	}
	return ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

// AddrIP returns the IP address of a.
func AddrIP(a Multiaddr) (net.IP, error) { return manet.ToIP(a) }

// SubscribeReachability subscribes to the changes of the host's
// reachability. Read the new value of an event with ReachabilityOf.
func (n *Host) SubscribeReachability() (Subscription, error) {
	return n.h.EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
}

// ReachabilityOf returns the reachability that an event from
// SubscribeReachability carries.
func ReachabilityOf(e any) Reachability {
	ev, _ := e.(event.EvtLocalReachabilityChanged)
	return ev.Reachability
}
