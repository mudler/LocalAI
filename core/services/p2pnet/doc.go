// Package p2pnet is LocalAI's peer-to-peer network layer: a lean libp2p host
// with a persisted identity, an allowlist that admits only paired peers, and
// helpers that turn libp2p streams into net.Conn values, HTTP clients and
// loopback forwarders.
//
// The host finds peers in this order: a direct connection, a hole punch that
// is coordinated through a relay, and a relayed connection. A relay may limit
// the data and the time of each relayed connection. Dial reads the limits
// that the relay reports and compares them with what the caller needs (see
// Need and PathLimitedError), so the caller decides what may use a limited
// path.
//
// A hint relay (see WithHints) is a public relay on which the peer holds a
// reservation. The host connects through it only to run a hole punch, and
// sends no application data through it: when no direct connection opens,
// Dial returns a NoDirectPathError. Once a dial named hints for a peer, the
// host treats every relay of that peer as such a relay, unless a dial to the
// same peer named it with WithRelays.
//
// Only this package and the relay packages import libp2p. Other code uses the
// types of this package and the peer.ID type.
package p2pnet
