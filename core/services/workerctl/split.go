package workerctl

import "time"

// The exchange of model-split workers between a backend node and the
// frontends, on POST /api/node/{id}/split. A split worker pairs with a
// frontend; the administrator attaches it to a backend node, which runs the
// split manager for it. The node sends its report at every heartbeat interval
// and gets the workers attached to it in the answer.

// SplitReport is the body that a backend node sends.
type SplitReport struct {
	// PeerID and Addrs reach the P2P host of the node. Both are empty while
	// the node runs no host (no worker is attached to it).
	PeerID string   `json:"peer_id,omitempty"`
	Addrs  []string `json:"addrs,omitempty"`
	// Workers is the live state of the workers attached to the node.
	Workers []SplitWorkerReport `json:"workers,omitempty"`
}

// SplitWorkerReport is the state of one split worker on a node.
type SplitWorkerReport struct {
	DeviceID string `json:"device_id"`
	// State is online, offline, slow, limited or refused.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Path is how the node reaches the worker.
	Path SplitPath `json:"path"`
	// OwnRelay is true when the relay of Path is a gated relay that the
	// frontend administers.
	OwnRelay    bool       `json:"own_relay,omitempty"`
	MemoryBytes uint64     `json:"memory_bytes,omitempty"`
	Name        string     `json:"name,omitempty"`
	Version     string     `json:"version,omitempty"`
	Engine      string     `json:"engine,omitempty"`
	Addr        string     `json:"addr,omitempty"`
	Rank        int        `json:"rank,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
}

// SplitPath is a P2P path as a node reports it.
type SplitPath struct {
	// Kind is none, direct, hole_punched or relayed.
	Kind        string `json:"kind"`
	Relay       string `json:"relay,omitempty"`
	Limited     bool   `json:"limited,omitempty"`
	LimitBytes  int64  `json:"limit_bytes,omitempty"`
	LimitSecond int64  `json:"limit_seconds,omitempty"`
	RTTMillis   int64  `json:"rtt_ms,omitempty"`
}

// SplitAssignment is the answer: the workers attached to the node.
type SplitAssignment struct {
	// SplitServer is the declaration of the node: only a split server runs
	// the P2P host for split workers. A node that is not one stops its host
	// and serves no worker; Workers is then empty.
	SplitServer bool            `json:"split_server"`
	Workers     []SplitAssigned `json:"workers"`
	// OwnRelays are the peer IDs of the gated relays that the frontend
	// administers. A worker behind one of them may be used after "Use
	// anyway" even when the relay limits each connection.
	OwnRelays []string `json:"own_relays,omitempty"`
	// Relays are the addresses (ending in /p2p/<relay peer ID>) of the gated
	// relays that the frontend administers and that admit this node. A node
	// that registered with the registration token keeps a reservation on
	// them, so that its split workers reach it through a relay when no
	// direct or hole-punched path works. Empty for a node that joined over
	// P2P, which uses the relays of its invite.
	Relays []string `json:"relays,omitempty"`
}

// SplitAssigned is one worker attached to the node.
type SplitAssigned struct {
	DeviceID      string    `json:"device_id"`
	Label         string    `json:"label,omitempty"`
	PeerID        string    `json:"peer_id"`
	Engine        string    `json:"engine"`
	AllowSlowPath bool      `json:"allow_slow_path,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}
