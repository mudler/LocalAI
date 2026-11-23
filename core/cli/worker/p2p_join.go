package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

// P2PJoinFlags join a model-split worker to a server over P2P.
type P2PJoinFlags struct {
	Join              string   `name:"join" env:"LOCALAI_P2P_JOIN" help:"Invite link of kind model-split worker (localai://pair?...) from the server this worker lends its memory to. The worker pairs once and keeps the pairing in --p2p-state-dir, so it can start again with the same link" group:"p2p"`
	P2PName           string   `name:"p2p-name" env:"LOCALAI_P2P_NAME" help:"Name of this worker on the server (default: the host name)" group:"p2p"`
	P2PAllowOpenRelay bool     `name:"p2p-allow-open-relay" env:"LOCALAI_P2P_ALLOW_OPEN_RELAY" help:"Connect through an open relay that the invite names. Anyone can use an open relay; the traffic is encrypted end to end, but the relay sees who talks to whom" group:"p2p"`
	P2PListen         []string `name:"p2p-listen" env:"LOCALAI_P2P_LISTEN_MADDRS" help:"Listen multiaddrs of the P2P host (default: TCP and QUIC on every interface, random ports). A fixed, open port gives the server a direct path" group:"p2p"`
	P2PBootstrap      []string `name:"p2p-bootstrap" env:"LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS" help:"Bootstrap peers of the P2P host, multiaddrs that end in /p2p/<peer ID> (default: the public bootstrap peers while the public DHT is on)" group:"p2p"`
	P2PNoPublicDHT    bool     `name:"p2p-no-public-dht" env:"LOCALAI_P2P_NO_PUBLIC_DHT" help:"Do not use the public DHT, which finds public relays that coordinate hole punching" group:"p2p"`
}

// joinRecord is the pairing a split worker keeps next to its identity.
type joinRecord struct {
	ServerPeer string   `json:"server_peer"`
	ServerName string   `json:"server_name,omitempty"`
	DeviceID   string   `json:"device_id"`
	Engine     string   `json:"engine"`
	Addrs      []string `json:"addrs,omitempty"`
	Relays     []string `json:"relays,omitempty"`
}

// joined is a P2P host paired with a server.
type joined struct {
	host   *p2pnet.Host
	server p2pnet.ID
	dial   []p2pnet.DialOption
	name   string
}

const joinRecordFile = "server.json"

// errNotSplitInvite is the error for an invite of another kind.
var errNotSplitInvite = errors.New("this invite is not for a model-split worker: on the server, choose Add a node, Model-split worker, and use the invite it makes")

// join pairs the worker with the server of the invite, or reuses the pairing
// saved in stateDir when it is with the same server, and starts the P2P host
// that the server reaches the worker by.
func (f *P2PJoinFlags) join(ctx context.Context, stateDir string, engine string) (*joined, error) {
	link, err := p2ppair.ParseLink(f.Join)
	if err != nil {
		return nil, fmt.Errorf("--join: %w", err)
	}
	if link.Kind != p2ppair.KindSplit {
		return nil, fmt.Errorf("%w (it is of kind %s)", errNotSplitInvite, link.Kind)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	name := f.P2PName
	if name == "" {
		name, _ = os.Hostname()
	}
	rec, err := readJoinRecord(stateDir)
	if err != nil {
		return nil, err
	}
	reuse := rec != nil && rec.ServerPeer == link.Peer.String()
	if reuse && rec.Engine != "" && rec.Engine != engine {
		return nil, fmt.Errorf("the pairing in %s is for the %s engine; use another --p2p-state-dir for this engine", stateDir, rec.Engine)
	}
	if !reuse {
		if link.Expired(time.Now()) {
			return nil, pairclient.ErrExpired
		}
		if len(link.Open) > 0 && !f.P2PAllowOpenRelay {
			return nil, fmt.Errorf("%w; pass --p2p-allow-open-relay to connect through it anyway", pairclient.ErrOpenRelay)
		}
	}

	cfg := p2pnet.Config{
		IdentityPath: filepath.Join(stateDir, "identity.key"),
		ListenAddrs:  f.P2PListen,
		PublicDHT:    !f.P2PNoPublicDHT,
		MDNS:         true,
		// Only the server may open streams to the worker.
		Gate:      p2pnet.NewAllowlist(link.Peer),
		UserAgent: "localai-worker/" + internal.Version,
	}
	boot, err := p2pnet.ParsePeerAddrs(f.P2PBootstrap)
	if err != nil {
		return nil, fmt.Errorf("--p2p-bootstrap: %w", err)
	}
	cfg.BootstrapPeers = boot
	// The worker keeps a reservation on the relays of the invite, so the
	// server can reach it when it cannot dial it directly.
	cfg.StaticRelays = link.Relays
	h, err := p2pnet.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	j := &joined{host: h, server: link.Peer, dial: pairclient.DialOptions(link), name: name}
	if reuse {
		xlog.Info("Using the saved pairing with the server", "server", rec.ServerPeer, "device", rec.DeviceID)
		return j, nil
	}

	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := pairclient.Redeem(rctx, h, link, pairclient.Request{Name: name, Platform: runtime.GOOS, AllowOpenRelay: f.P2PAllowOpenRelay})
	if err != nil {
		_ = h.Close()
		return nil, err
	}
	rec = &joinRecord{ServerPeer: link.Peer.String(), ServerName: out.Server.Name, DeviceID: out.DeviceID, Engine: engine}
	for _, a := range link.Addrs {
		rec.Addrs = append(rec.Addrs, a.String())
	}
	for _, r := range link.Relays {
		for _, a := range r.Addrs {
			rec.Relays = append(rec.Relays, a.String()+"/p2p/"+r.ID.String())
		}
	}
	if err := writeJoinRecord(stateDir, rec); err != nil {
		_ = h.Close()
		return nil, err
	}
	server := out.Server.Name
	if server == "" {
		server = link.Peer.String()
	}
	fmt.Printf("Paired with %s as a model-split worker.\n", server)
	fmt.Printf("Confirmation phrase: %s\n", out.Phrase)
	fmt.Println("Check that the server shows the same four words. If they differ, the link was swapped: revoke this worker on the server.")
	return j, nil
}

func readJoinRecord(dir string) (*joinRecord, error) {
	b, err := os.ReadFile(filepath.Join(dir, joinRecordFile)) // #nosec G304 -- dir is the worker's own state directory and the file name is a constant
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec joinRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, joinRecordFile), err)
	}
	return &rec, nil
}

func writeJoinRecord(dir string, rec *joinRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, joinRecordFile), b, 0o600)
}

// workerMemory is the memory a worker lends: its VRAM, else its RAM (which
// is also the memory of Apple Silicon).
func workerMemory(preferVRAM bool) uint64 {
	if preferVRAM {
		if v, err := xsysinfo.TotalAvailableVRAM(); err == nil && v > 0 {
			return v
		}
	}
	if ram, err := xsysinfo.GetSystemRAMInfo(); err == nil {
		return ram.Total
	}
	return 0
}
