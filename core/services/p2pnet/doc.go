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
// Only this package and the relay packages import libp2p. Other code uses the
// types of this package and the peer.ID type.
package p2pnet
