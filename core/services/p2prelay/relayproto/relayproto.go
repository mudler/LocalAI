// Package relayproto holds the wire types and the framing of the two LocalAI
// relay protocols:
//
//   - AdminID (/localai/relay-admin/1): a LocalAI server, authenticated by its
//     peer ID, manages its own set of members on a gated relay.
//   - PassID (/localai/relay-pass/1): a peer that holds a relay pass from an
//     invite asks the relay to admit it as a pending member.
//
// Each stream carries one request and one response. A message is an unsigned
// varint length followed by that many bytes of JSON, at most MaxMessageSize.
// Inside version 1 the field "v" must be 1 and unknown fields are ignored, so
// only additive changes are allowed; a breaking change needs a new protocol ID.
package relayproto

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	// AdminID is the protocol of the admin requests.
	AdminID protocol.ID = "/localai/relay-admin/1"
	// PassID is the protocol on which a stranger presents a relay pass.
	PassID protocol.ID = "/localai/relay-pass/1" // #nosec G101 -- a protocol name, not a credential

	// Version is the value of the "v" field of every message.
	Version = 1
)

// Operations of the admin protocol.
const (
	// OpStatus returns the relay status and the caller's set summary.
	OpStatus = "status"
	// OpList returns the caller's set with labels.
	OpList = "list"
	// OpAdd adds peers to the caller's set. A pending member becomes permanent.
	OpAdd = "add"
	// OpRemove removes peers from the caller's set and closes their connections.
	OpRemove = "remove"
	// OpReplace sets the caller's permanent members to exactly the given peers.
	OpReplace = "replace"
	// OpClaim makes the caller an admin of an unclaimed relay with the claim code.
	OpClaim = "claim"
	// OpPass registers a single-use relay pass with a TTL.
	OpPass = "pass"
	// OpPassCancel removes a pass and the pending member it admitted, if any.
	OpPassCancel = "pass-cancel"
	// OpLimits reads the per-circuit limits of the relay and, when the
	// request carries a LimitsPatch, changes them.
	OpLimits = "limits"
)

// Relay modes reported in Status.Mode.
const (
	ModeGated     = "gated"
	ModeOpen      = "open"
	ModeUnclaimed = "unclaimed"
)

// Peer is a member of an admin's set.
type Peer struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	// Pending is set on members admitted by a relay pass that the admin has
	// not confirmed with add or replace yet.
	Pending bool `json:"pending,omitempty"`
}

// Pass is the argument of OpPass and OpPassCancel. OpPassCancel needs only Code.
type Pass struct {
	Code       string `json:"code"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Label      string `json:"label,omitempty"`
}

// Request is an admin request.
type Request struct {
	V   int    `json:"v"`
	Op  string `json:"op"`
	Seq int64  `json:"seq,omitempty"`
	// Peers is the argument of add, remove and replace.
	Peers []Peer `json:"peers,omitempty"`
	// Code is the claim code of OpClaim.
	Code string `json:"code,omitempty"`
	Pass *Pass  `json:"pass,omitempty"`
	// Limits is the argument of OpLimits. Without it the request only reads.
	Limits *LimitsPatch `json:"limits,omitempty"`
}

// LimitsPatch changes the limits of a relay. A nil field stays as it is; a
// zero value removes that limit.
type LimitsPatch struct {
	CircuitMaxBytes   *int64 `json:"circuit_max_bytes,omitempty"`
	CircuitMaxSeconds *int64 `json:"circuit_max_seconds,omitempty"`
	RateBPS           *int64 `json:"rate_bps,omitempty"`
}

// LimitsCeiling is the highest value an admin may set for each limit. The
// operator of the relay sets it with the relay's flags. Zero means no
// ceiling for that limit. With a ceiling, an admin cannot remove the limit
// (set it to zero) either.
type LimitsCeiling struct {
	CircuitMaxBytes   int64 `json:"circuit_max_bytes"`
	CircuitMaxSeconds int64 `json:"circuit_max_seconds"`
	RateBPS           int64 `json:"rate_bps"`
}

// Limits are the per-circuit limits of a relay, as OpLimits reports them.
// Zero means no limit.
type Limits struct {
	// CircuitMaxBytes is the data limit per circuit and direction.
	CircuitMaxBytes int64 `json:"circuit_max_bytes"`
	// CircuitMaxSeconds is the time limit per circuit.
	CircuitMaxSeconds int64 `json:"circuit_max_seconds"`
	// RateBPS is the bandwidth limit per circuit and direction, in bytes per
	// second.
	RateBPS int64 `json:"rate_bps"`
	// Ceiling is what an admin may set at most. OpLimits refuses a patch
	// above it with an error that names the ceiling.
	Ceiling LimitsCeiling `json:"ceiling"`
	// Restarted is set when applying the change restarted the circuit
	// service: reservations are dropped and peers reserve again within
	// moments. A change of the bandwidth limit alone never restarts it.
	Restarted bool `json:"restarted,omitempty"`
}

// Response is the answer to a Request. When OK is false, Err says why.
type Response struct {
	V     int    `json:"v"`
	OK    bool   `json:"ok"`
	Err   string `json:"err,omitempty"`
	Admin bool   `json:"admin,omitempty"`
	// Seq is the stored sequence number of the caller's set.
	Seq int64 `json:"seq,omitempty"`
	// Digest is Digest of the caller's permanent members.
	Digest string `json:"digest,omitempty"`
	Count  int    `json:"count"`
	// MaxPeers is how many peers the set of one admin may hold, pending
	// members included. It is sent to admins only, so that a server can
	// choose which peers to leave out when its list does not fit.
	MaxPeers int `json:"max_peers,omitempty"`
	// Peers is filled by OpList.
	Peers []Peer `json:"peers,omitempty"`
	// Status is filled by OpStatus.
	Status *Status `json:"status,omitempty"`
	// Limits is filled by OpLimits.
	Limits *Limits `json:"limits,omitempty"`
}

// Status describes a relay. Admins get every field; members get only the
// public ones (version, mode, peer ID, circuit limits, rate limit, their ceiling, your_addr).
type Status struct {
	Version  string `json:"version"`
	Mode     string `json:"mode"`
	PeerID   string `json:"peer_id"`
	Admins   int    `json:"admins,omitempty"`
	Members  int    `json:"members,omitempty"`
	Pending  int    `json:"pending,omitempty"`
	Passes   int    `json:"passes,omitempty"`
	Reserv   int64  `json:"reservations,omitempty"`
	Circuits int64  `json:"circuits,omitempty"`
	// CircuitsTotal counts the circuits opened since start.
	CircuitsTotal int64 `json:"circuits_total,omitempty"`
	// Denied counts refused circuit requests since start.
	Denied       int64 `json:"denied,omitempty"`
	BytesRelayed int64 `json:"bytes_relayed,omitempty"`
	// CircuitMaxBytes is the data limit per circuit and direction (0: none).
	CircuitMaxBytes int64 `json:"circuit_max_bytes"`
	// CircuitMaxSeconds is the time limit per circuit (0: none).
	CircuitMaxSeconds int64 `json:"circuit_max_seconds"`
	// RateBPS is the bandwidth limit per circuit and direction in bytes per
	// second (0: none).
	RateBPS int64 `json:"rate_bps"`
	// Ceiling is the highest value an admin may set for each of the three
	// limits above (0: no ceiling).
	Ceiling LimitsCeiling `json:"ceiling"`
	// TotalRateBPS is the bandwidth limit of the whole relay (0: none).
	TotalRateBPS    int64 `json:"total_rate_bps,omitempty"`
	MaxCircuits     int   `json:"max_circuits,omitempty"`
	MaxReservations int   `json:"max_reservations,omitempty"`
	UptimeSeconds   int64 `json:"uptime_seconds,omitempty"`
	// YourAddr is the caller's address as the relay sees it.
	YourAddr string `json:"your_addr,omitempty"`
}

// PassRequest is sent on PassID.
type PassRequest struct {
	V    int    `json:"v"`
	Code string `json:"code"`
}

// PassResponse is the answer on PassID. A refused pass always carries the
// same error, so a caller cannot tell an unknown code from a used or an
// expired one.
type PassResponse struct {
	V   int    `json:"v"`
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
	// TTLSeconds is how long the pending membership lasts unless the admin
	// confirms it.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
}

// Digest is the in-sync check of a set: the hex SHA-256 of the sorted peer
// IDs joined by newlines. The server and the relay compute it on their own
// copy and compare.
func Digest(ids []peer.ID) string {
	s := make([]string, 0, len(ids))
	for _, id := range ids {
		s = append(s, id.String())
	}
	slices.Sort(s)
	h := sha256.Sum256([]byte(strings.Join(s, "\n")))
	return hex.EncodeToString(h[:])
}
