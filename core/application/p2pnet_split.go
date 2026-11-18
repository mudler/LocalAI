package application

import (
	"context"
	"fmt"
	"slices"

	"github.com/mudler/xlog"

	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2psplit"
)

// splitPorts keeps the loopback port of each split worker for the life of the
// process: a model that llama.cpp loaded with an address keeps working when
// the P2P host restarts.
//
// Distributed mode: each frontend runs its own split manager on its own host
// (state per instance). A split worker keeps one control session, to the
// frontend it reaches; the devices are shared through the pairing store.
var splitPorts = p2psplit.NewPortBook()

// startSplit runs the model-split manager of mode share.
func (rt *p2pRuntime) startSplit() error {
	cfg := rt.net.app.ApplicationConfig()
	m, err := p2psplit.NewManager(rt.ctx, p2psplit.Config{
		Host:    rt.host,
		Devices: rt.splitDevices,
		OnLlama: cfg.LlamaCPPTunnelCallback,
		OnMLX:   cfg.MLXTunnelCallback,
		Ports:   splitPorts,
	})
	if err != nil {
		return fmt.Errorf("p2p: model-split workers: %w", err)
	}
	rt.split = m
	return nil
}

func (rt *p2pRuntime) stopSplit() {
	if rt.split == nil {
		return
	}
	if err := rt.split.Close(); err != nil {
		xlog.Debug("P2P split manager close", "error", err)
	}
}

func (rt *p2pRuntime) refreshSplit() {
	if rt.split != nil {
		rt.split.Refresh()
	}
}

// splitDevices lists the active devices of kind split.
func (rt *p2pRuntime) splitDevices(ctx context.Context) ([]p2psplit.Device, error) {
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	var out []p2psplit.Device
	for _, d := range devs {
		if d.Kind != p2ppair.KindSplit || d.RevokedAt != nil {
			continue
		}
		p, err := p2pnet.DecodePeerID(d.PeerID)
		if err != nil {
			continue
		}
		out = append(out, p2psplit.Device{
			ID: d.ID, Label: d.Label, Peer: p, Engine: splitEngine(d.Scope),
			AllowSlowPath: d.AllowSlowPath, CreatedAt: d.CreatedAt,
		})
	}
	return out, nil
}

// splitEngine is the engine a split invite named; invites made before the
// engine was recorded default to llama.cpp.
func splitEngine(s p2ppair.Scope) p2psplit.Engine {
	if e := p2psplit.Engine(s.Labels[p2psplit.EngineLabel]); e.Valid() {
		return e
	}
	return p2psplit.EngineLlama
}

// splitInviteLabels checks the engine label of a split invite and sets the
// default engine, llama.cpp.
func splitInviteLabels(labels map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	e := p2psplit.Engine(out[p2psplit.EngineLabel])
	switch {
	case e == "":
		out[p2psplit.EngineLabel] = string(p2psplit.EngineLlama)
	case !e.Valid():
		return nil, fmt.Errorf("%w: labels.engine must be %q or %q", p2papi.ErrInvalid, p2psplit.EngineLlama, p2psplit.EngineMLX)
	}
	return out, nil
}

var _ p2papi.SplitService = p2pService{}

// P2PSplit returns the model-split workers as the HTTP handlers see them.
func (a *Application) P2PSplit() p2papi.SplitService { return p2pService{n: a.p2p()} }

func (s p2pService) ListSplitWorkers(ctx context.Context) (p2papi.SplitWorkerList, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.SplitWorkerList{}, err
	}
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return p2papi.SplitWorkerList{}, err
	}
	labels := rt.relayLabels()
	out := p2papi.SplitWorkerList{Workers: []p2papi.SplitWorkerView{}}
	for _, w := range rt.split.Workers() {
		i := slices.IndexFunc(devs, func(d p2ppair.Device) bool { return d.ID == w.DeviceID })
		if i < 0 {
			continue
		}
		v := splitView(w, devs[i], labels)
		if v.State == string(p2psplit.StateOnline) {
			out.MemoryInUse += v.MemoryBytes
		}
		out.Workers = append(out.Workers, v)
	}
	return out, nil
}

func (s p2pService) SetSplitWorker(ctx context.Context, id string, req p2papi.SplitWorkerUpdate) (p2papi.SplitWorkerView, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.SplitWorkerView{}, err
	}
	dev, err := rt.splitDevice(ctx, id)
	if err != nil {
		return p2papi.SplitWorkerView{}, err
	}
	if req.AllowSlowPath != nil {
		if err := rt.pair.SetAllowSlowPath(ctx, id, *req.AllowSlowPath); err != nil {
			return p2papi.SplitWorkerView{}, pairErr(err)
		}
		dev.AllowSlowPath = *req.AllowSlowPath
		xlog.Info("P2P split worker slow path", "worker", dev.Label, "allow", dev.AllowSlowPath)
		// Every frontend reloads the devices; this one now.
		rt.changed("split")
	}
	labels := rt.relayLabels()
	for _, w := range rt.split.Workers() {
		if w.DeviceID == id {
			w.AllowSlowPath = dev.AllowSlowPath
			return splitView(w, dev, labels), nil
		}
	}
	return p2papi.SplitWorkerView{}, p2papi.ErrNotFound
}

// splitDevice returns the active split device id.
func (rt *p2pRuntime) splitDevice(ctx context.Context, id string) (p2ppair.Device, error) {
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return p2ppair.Device{}, err
	}
	i := slices.IndexFunc(devs, func(d p2ppair.Device) bool {
		return d.ID == id && d.Kind == p2ppair.KindSplit && d.RevokedAt == nil
	})
	if i < 0 {
		return p2ppair.Device{}, p2papi.ErrNotFound
	}
	return devs[i], nil
}

// relayLabels maps relay peer IDs to the labels of the relays the server knows.
func (rt *p2pRuntime) relayLabels() map[string]string {
	labels := map[string]string{}
	for _, r := range rt.relayList() {
		if ai, err := p2pnet.ParsePeerAddr(r.Addr); err == nil {
			labels[ai.ID.String()] = r.Label
		}
	}
	return labels
}

func splitView(w p2psplit.Worker, d p2ppair.Device, labels map[string]string) p2papi.SplitWorkerView {
	v := p2papi.SplitWorkerView{
		ID: w.DeviceID, Label: w.Label, PeerID: w.Peer, Engine: string(w.Engine), State: string(w.State),
		Error: w.Error, AllowSlowPath: w.AllowSlowPath, MemoryBytes: w.MemoryBytes, Name: w.Name,
		Version: w.Version, Addr: w.Addr, Rank: w.Rank, ConnectedSince: w.Since,
		CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt, Path: p2papi.PathView{Kind: string(p2pnet.PathNone)},
	}
	if w.Path.Kind != p2pnet.PathNone {
		v.Path = pathView(w.Path, labels)
	}
	return v
}
