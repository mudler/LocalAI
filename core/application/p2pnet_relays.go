package application

import (
	"context"
	"time"

	ma "github.com/multiformats/go-multiaddr"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

// relayManager is what the P2P layer needs from the relay manager: the list of
// relays, the admin operations of the API, relay passes for invites, and the
// pairing changes it pushes to every relay. The relay manager of package
// p2prelaymgr implements it through a small adapter that converts its types
// to the API types; p2pNet.relayFactory builds it. Without one, the relays of
// the p2p_relays setting are listed and the admin operations answer 503.
type relayManager interface {
	List() []p2papi.Relay
	Add(ctx context.Context, addr, label, claimCode string) (p2papi.Relay, error)
	Remove(ctx context.Context, id string) error
	Check(ctx context.Context, id string) (p2papi.RelayCheck, error)
	Sync(ctx context.Context, id string) (p2papi.Relay, error)
	Claim(ctx context.Context, id, code string) (p2papi.Relay, error)
	RegisterPass(ctx context.Context, relayID, code string, ttl time.Duration) error
	CancelPass(ctx context.Context, relayID, code string) error
	PeerAdded(peer string)
	PeerRemoved(peer string)
	Start(ctx context.Context) error
	Close() error
}

// relayManagerConfig is what the P2P layer hands to the relay manager.
type relayManagerConfig struct {
	Host *p2pnet.Host
	// Relays is the p2p_relays setting at start.
	Relays []config.P2PRelay
	// PairedPeers lists the peer IDs of the active devices.
	PairedPeers func() []string
}

// passRegistrar registers the relay pass of an invite on a relay. The pass is
// derived from the invite ID (see passCode).
type passRegistrar struct {
	rm     relayManager
	secret []byte
}

func (r *passRegistrar) RegisterPass(ctx context.Context, req p2ppair.PassRequest) (string, error) {
	code := passCode(r.secret, req.InviteID)
	if err := r.rm.RegisterPass(ctx, req.Relay.ID, code, req.TTL); err != nil {
		return "", err
	}
	return code, nil
}

func (r *passRegistrar) CancelPass(ctx context.Context, relay p2ppair.Relay, inviteID string) error {
	return r.rm.CancelPass(ctx, relay.ID, passCode(r.secret, inviteID))
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
		addr, err := ma.NewMultiaddr(r.Addr)
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
