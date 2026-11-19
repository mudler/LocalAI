package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

// relayManager is what the P2P layer needs from the relay manager: the list of
// relays, the admin operations of the API, the registrar of relay passes for
// invites, and the pairing changes it pushes to every relay. The relay manager
// of package p2prelaymgr implements it through relayManagerAdapter;
// p2pNet.relayFactory builds it. Without one, the relays of the p2p_relays
// setting are listed without passes and the admin operations answer 503.
type relayManager interface {
	List() []p2papi.Relay
	Add(ctx context.Context, addr, label, claimCode string) (p2papi.Relay, error)
	Remove(ctx context.Context, id string) error
	Check(ctx context.Context, id string) (p2papi.RelayCheck, error)
	Sync(ctx context.Context, id string) (p2papi.Relay, error)
	Claim(ctx context.Context, id, code string) (p2papi.Relay, error)
	Limits(ctx context.Context, id string) (p2papi.RelayLimitsInfo, error)
	SetLimits(ctx context.Context, id string, req p2papi.SetRelayLimitsRequest) (p2papi.RelayLimitsInfo, error)
	Registrar() p2ppair.PassRegistrar
	PeerAdded(peer string)
	PeerRemoved(peer string)
	// Resync brings every relay in line with the paired devices and the node
	// peers, in the background.
	Resync()
	// NodeAdmission is the state of a node peer on each relay.
	NodeAdmission(peer string) []p2papi.SplitRelayAdmission
	Start(ctx context.Context) error
	Close() error
}

// relayManagerConfig is what the P2P layer hands to the relay manager.
type relayManagerConfig struct {
	Host *p2pnet.Host
	// LoadRelays reads the p2p_relays setting.
	LoadRelays func() []config.P2PRelay
	// SaveRelays stores the relays in the p2p_relays setting.
	SaveRelays func([]config.P2PRelay) error
	// PairedPeers lists the peer IDs of the active devices.
	PairedPeers func() []string
	// NodePeers lists the peer IDs of the split hosts of the backend nodes
	// that registered with the registration token and serve split workers.
	NodePeers func() []string
}

// Relays implements p2ppair.RelaySource: the relays an invite may name, gated
// ones first. Relays that are not claimed or whose mode is unknown give no
// pass and are left out; without a relay manager the relays of the setting
// are named without a pass.
func (rt *p2pRuntime) Relays(context.Context) []p2ppair.Relay {
	var out []p2ppair.Relay
	for _, r := range rt.relayList() {
		if rt.relays != nil && r.Mode != "gated" && r.Mode != "open" {
			continue
		}
		addr, err := p2pnet.ParseMultiaddr(r.Addr)
		if err != nil {
			continue
		}
		out = append(out, p2ppair.Relay{ID: r.ID, Label: r.Label, Addr: addr, Open: r.Mode == "open"})
	}
	return out
}

// relayList lists the relays from the manager, or from the setting.
func (rt *p2pRuntime) relayList() []p2papi.Relay {
	if rt.relays != nil {
		return rt.relays.List()
	}
	out := make([]p2papi.Relay, 0, len(rt.cfg.Relays))
	for _, r := range rt.cfg.Relays {
		out = append(out, p2papi.Relay{ID: r.ID, Label: r.Label, Addr: r.Addr, Mode: "unknown", State: "unknown"})
	}
	return out
}
