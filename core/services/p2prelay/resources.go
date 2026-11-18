package p2prelay

import (
	"fmt"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/mudler/xlog"
)

// newResourceManager returns the resource manager of the relay host: room for
// the circuits, a per-IP connection cap, and the stream filter of the gate.
func newResourceManager(cfg *Config, g *gate) (network.ResourceManager, error) {
	limits := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&limits)
	streams := 4096
	if cfg.MaxCircuits > 0 {
		// Two streams per circuit, plus room for reservations.
		streams = 2*cfg.MaxCircuits + 64
	}
	limits.AddServiceLimit(relayv2.ServiceName,
		rcmgr.BaseLimit{StreamsInbound: streams, StreamsOutbound: streams, Streams: 2 * streams, Memory: 64 << 20},
		rcmgr.BaseLimitIncrease{})
	perIP := cfg.MaxConnsPerIP
	if perIP == 0 {
		perIP = 1 << 20
	}
	rm, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(limits.AutoScale()),
		rcmgr.WithLimitPerSubnet(
			[]rcmgr.ConnLimitPerSubnet{{PrefixLength: 32, ConnCount: perIP}},
			[]rcmgr.ConnLimitPerSubnet{{PrefixLength: 56, ConnCount: perIP}}))
	if err != nil {
		return nil, fmt.Errorf("p2prelay: resource manager: %w", err)
	}
	return &gatedResourceManager{ResourceManager: rm, gate: g}, nil
}

// gatedResourceManager refuses the streams of peers that are not admitted,
// except the claim or pass stream the gate allows them. The host sets the
// protocol of a stream on its scope right after the protocol negotiation and
// resets the stream when that fails, so this is the one place where every
// stream of every protocol can be checked before its handler runs.
type gatedResourceManager struct {
	network.ResourceManager
	gate *gate
}

func (m *gatedResourceManager) OpenStream(p peer.ID, dir network.Direction) (network.StreamManagementScope, error) {
	s, err := m.ResourceManager.OpenStream(p, dir)
	if err != nil {
		return nil, err
	}
	return &gatedStreamScope{StreamManagementScope: s, peer: p, dir: dir, gate: m.gate}, nil
}

type gatedStreamScope struct {
	network.StreamManagementScope
	peer peer.ID
	dir  network.Direction
	gate *gate
}

var errStrangerStream = fmt.Errorf("p2prelay: stream refused: peer is not a member")

func (s *gatedStreamScope) SetProtocol(proto protocol.ID) error {
	if !s.allowed(proto) {
		xlog.Debug("relay: deny stream", "peer", s.peer, "protocol", proto, "reason", "not paired")
		return errStrangerStream
	}
	return s.StreamManagementScope.SetProtocol(proto)
}

func (s *gatedStreamScope) allowed(proto protocol.ID) bool {
	if s.gate.admitted(s.peer) {
		return true
	}
	if s.dir == network.DirOutbound {
		// The host asks every new peer for its identify record. That sends
		// nothing about the relay; identify push, which does, is refused.
		return proto == identify.ID
	}
	return s.gate.strangerProtocol(proto)
}
