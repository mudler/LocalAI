package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
)

// newRelayManager builds the relay manager of mode share on the p2p_relays
// setting.
func newRelayManager(c relayManagerConfig) (relayManager, error) {
	m, err := p2prelaymgr.New(p2prelaymgr.Config{
		Host:        c.Host,
		Store:       settingsRelayStore{load: c.LoadRelays, save: c.SaveRelays},
		PairedPeers: c.PairedPeers,
		NodePeers:   c.NodePeers,
	})
	if err != nil {
		return nil, err
	}
	return relayManagerAdapter{m: m}, nil
}

// settingsRelayStore keeps the relays of the manager in the p2p_relays
// setting.
type settingsRelayStore struct {
	load func() []config.P2PRelay
	save func([]config.P2PRelay) error
}

func (s settingsRelayStore) Load(context.Context) ([]p2prelaymgr.Entry, error) {
	var out []p2prelaymgr.Entry
	for _, r := range s.load() {
		out = append(out, p2prelaymgr.Entry{ID: r.ID, Label: r.Label, Addr: r.Addr})
	}
	return out, nil
}

func (s settingsRelayStore) Save(_ context.Context, entries []p2prelaymgr.Entry) error {
	relays := make([]config.P2PRelay, 0, len(entries))
	for _, e := range entries {
		relays = append(relays, config.P2PRelay{ID: e.ID, Label: e.Label, Addr: e.Addr})
	}
	return s.save(relays)
}

// relayManagerAdapter turns the manager's types into the API types and its
// errors into the API errors.
type relayManagerAdapter struct{ m p2prelaymgr.Manager }

func relayView(r p2prelaymgr.Relay) p2papi.Relay {
	v := p2papi.Relay{
		ID: r.ID, Label: r.Label, Addr: r.Addr, Mode: r.Mode, State: r.State, Health: r.Health,
		LatencyMS: r.Latency.Milliseconds(), Admins: r.Admins, Members: r.Members,
		Limits: p2papi.RelayLimits{CircuitBytes: r.Limits.CircuitBytes, CircuitSeconds: r.Limits.CircuitSeconds, RateBPS: r.Limits.RateBPS},
	}
	if !r.CheckedAt.IsZero() {
		t := r.CheckedAt
		v.CheckedAt = &t
	}
	return v
}

func relayErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, p2prelaymgr.ErrNotFound):
		return p2papi.ErrNotFound
	case errors.Is(err, p2prelaymgr.ErrBadAddr):
		return fmt.Errorf("%w: %s", p2papi.ErrInvalid, err.Error())
	case errors.Is(err, p2prelaymgr.ErrNotAdmin):
		return fmt.Errorf("%w: this server is not an admin of the relay, or the relay is open", p2papi.ErrConflict)
	case errors.Is(err, p2prelaymgr.ErrInvalidLimits):
		return fmt.Errorf("%w: %s", p2papi.ErrInvalid, err.Error())
	case errors.Is(err, p2prelaymgr.ErrClaimRefused):
		return fmt.Errorf("%w: the relay refused the claim code", p2papi.ErrConflict)
	}
	return err
}

func (a relayManagerAdapter) List() []p2papi.Relay {
	rs := a.m.List()
	out := make([]p2papi.Relay, 0, len(rs))
	for _, r := range rs {
		out = append(out, relayView(r))
	}
	return out
}

func (a relayManagerAdapter) Add(ctx context.Context, addr, label, claimCode string) (p2papi.Relay, error) {
	r, err := a.m.Add(ctx, addr, label, claimCode)
	return relayView(r), relayErr(err)
}

func (a relayManagerAdapter) Remove(ctx context.Context, id string) error {
	return relayErr(a.m.Remove(ctx, id))
}

func (a relayManagerAdapter) Check(ctx context.Context, id string) (p2papi.RelayCheck, error) {
	c, err := a.m.Check(ctx, id)
	if err != nil {
		return p2papi.RelayCheck{}, relayErr(err)
	}
	return p2papi.RelayCheck{
		Connected: c.Connected, Reserved: c.Reserved, AdminOK: c.AdminOK, CircuitOK: c.CircuitOK, InSync: c.InSync,
		Detail: c.Detail, Relay: relayView(c.Relay),
	}, nil
}

func (a relayManagerAdapter) Sync(ctx context.Context, id string) (p2papi.Relay, error) {
	r, err := a.m.Sync(ctx, id)
	return relayView(r), relayErr(err)
}

func (a relayManagerAdapter) Claim(ctx context.Context, id, code string) (p2papi.Relay, error) {
	r, err := a.m.Claim(ctx, id, code)
	return relayView(r), relayErr(err)
}

func limitsView(i p2prelaymgr.LimitsInfo) p2papi.RelayLimitsInfo {
	return p2papi.RelayLimitsInfo{
		Limits:    p2papi.RelayLimits{CircuitBytes: i.Limits.CircuitBytes, CircuitSeconds: i.Limits.CircuitSeconds, RateBPS: i.Limits.RateBPS},
		Ceiling:   p2papi.RelayLimits{CircuitBytes: i.Ceiling.CircuitBytes, CircuitSeconds: i.Ceiling.CircuitSeconds, RateBPS: i.Ceiling.RateBPS},
		CanChange: i.CanChange,
		Restarted: i.Restarted,
	}
}

func (a relayManagerAdapter) Limits(ctx context.Context, id string) (p2papi.RelayLimitsInfo, error) {
	i, err := a.m.Limits(ctx, id)
	return limitsView(i), relayErr(err)
}

func (a relayManagerAdapter) SetLimits(ctx context.Context, id string, req p2papi.SetRelayLimitsRequest) (p2papi.RelayLimitsInfo, error) {
	i, err := a.m.SetLimits(ctx, id, p2prelaymgr.LimitsPatch{
		CircuitBytes: req.CircuitBytes, CircuitSeconds: req.CircuitSeconds, RateBPS: req.RateBPS,
	})
	return limitsView(i), relayErr(err)
}

func (a relayManagerAdapter) Registrar() p2ppair.PassRegistrar {
	return p2prelaymgr.NewPassRegistrar(a.m)
}
func (a relayManagerAdapter) Resync() { a.m.Resync() }

func (a relayManagerAdapter) NodeAdmission(peer string) []p2papi.SplitRelayAdmission {
	var out []p2papi.SplitRelayAdmission
	for _, ad := range a.m.NodeAdmission(peer) {
		out = append(out, p2papi.SplitRelayAdmission{RelayID: ad.RelayID, Label: ad.Label, State: ad.State, Reason: ad.Reason})
	}
	return out
}

func (a relayManagerAdapter) PeerAdded(peer string)           { a.m.PeerAdded(peer) }
func (a relayManagerAdapter) PeerRemoved(peer string)         { a.m.PeerRemoved(peer) }
func (a relayManagerAdapter) Start(ctx context.Context) error { return a.m.Start(ctx) }
func (a relayManagerAdapter) Close() error                    { return a.m.Close() }
