package p2pnet

import (
	"fmt"
	"net"
	"strings"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
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

// IsPublicAddr says whether a is a public IP address.
func IsPublicAddr(a Multiaddr) bool { return manet.IsPublicAddr(a) }

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
