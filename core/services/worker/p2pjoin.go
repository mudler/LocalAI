package worker

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// A worker that joins with a worker invite (`local-ai worker --join <link>`)
// reaches the cluster over P2P only. It redeems the invite once, keeps its
// identity and the address of the frontend in its data directory, registers
// and heartbeats over P2P HTTP with its peer ID in place of the registration
// token, and opens its tunnel as one P2P stream per lane. It needs no inbound
// port and no address that the frontends can dial.
//
// Such a worker is tunnel-only: it never attaches to NATS. When the cluster
// runs on NATS the frontends reach it through its tunnel, and when the cluster
// changes to NATS it reports in follow_error why it stays on the tunnel.

// p2pJoinTimeout bounds the start of the host and the redeem of the invite.
const p2pJoinTimeout = 2 * time.Minute

// p2pCallTimeout bounds a call of the worker to the frontend over P2P.
const p2pCallTimeout = 30 * time.Second

// p2pHelloTimeout bounds the hello of a tunnel stream and its reply.
const p2pHelloTimeout = 15 * time.Second

// bulkNeed is what the bulk lane needs from a relayed path: no data limit.
// A public relay never carries model files.
var bulkNeed = p2pnet.NeedBulk

// joinRecord is what a worker keeps about the frontend it joined. It has the
// shape of the record of `local-ai p2p pair`.
type joinRecord struct {
	ServerPeer string   `json:"server_peer"`
	ServerName string   `json:"server_name,omitempty"`
	Kind       string   `json:"kind"`
	DeviceID   string   `json:"device_id"`
	Relays     []string `json:"relays,omitempty"`
	Addrs      []string `json:"addrs,omitempty"`
	// Invite is the hash of the code of the invite that was redeemed. A
	// different invite for the same server, made after the device was
	// revoked, is redeemed again. A record without it came from an older
	// version and matches any invite.
	Invite string `json:"invite,omitempty"`
}

// p2pJoin is the P2P side of a worker that joined with an invite.
type p2pJoin struct {
	host *p2pnet.Host
	// gate admits the frontend, and the model-split workers attached to
	// this node.
	gate   *p2pnet.Allowlist
	server p2pnet.ID
	dial   []p2pnet.DialOption
}

// identityPath returns the key file of the P2P identity of the worker.
func (c Config) identityPath() string {
	if c.P2PIdentity != "" {
		return c.P2PIdentity
	}
	return filepath.Join(c.ModelsPath, "..", "data", "p2p", "identity.key")
}

// joinP2P starts the P2P host of the worker and redeems the invite, unless this
// worker already joined with this invite. A worker restarts with the same
// command line, so a used invite that has a record is not redeemed again. A
// new invite of the same server is redeemed: the old device may be revoked.
func joinP2P(ctx context.Context, cfg *Config) (*p2pJoin, error) {
	link, err := p2ppair.ParseLink(cfg.Join)
	if err != nil {
		return nil, fmt.Errorf("reading the invite of --join: %w", err)
	}
	if link.Kind != p2ppair.KindWorker {
		return nil, fmt.Errorf("the invite of --join is a %s invite: ask for a worker invite (Add a node, P2P invite)", link.Kind)
	}
	identity := cfg.identityPath()
	recPath := filepath.Join(filepath.Dir(identity), "pairs", link.Peer.String()+".json")
	rec, err := readJoinRecord(recPath)
	if err != nil {
		return nil, err
	}
	redeem := rec == nil || (rec.Invite != "" && rec.Invite != inviteHash(link))
	if redeem {
		if link.Expired(time.Now()) {
			return nil, pairclient.ErrExpired
		}
		if len(link.Open) > 0 && !cfg.AllowOpenRelay {
			return nil, fmt.Errorf("%w; pass --allow-open-relay to connect through it anyway", pairclient.ErrOpenRelay)
		}
	}

	boot, err := p2pnet.ParsePeerAddrs(cfg.P2PBootstrap)
	if err != nil {
		return nil, fmt.Errorf("--p2p-bootstrap: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, p2pJoinTimeout)
	defer cancel()
	gate := p2pnet.NewAllowlist(link.Peer)
	h, err := p2pnet.New(ctx, p2pnet.Config{
		IdentityPath:   identity,
		ListenAddrs:    cfg.P2PListen,
		PublicDHT:      true,
		BootstrapPeers: boot,
		MDNS:           true,
		// The frontend may connect back for a hole punch. Nobody else may,
		// except the model-split workers attached to this node.
		Gate:      gate,
		UserAgent: "localai-worker/" + internal.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("starting the P2P host of the worker: %w", err)
	}
	j := &p2pJoin{host: h, gate: gate, server: link.Peer}

	if redeem {
		name := cmp.Or(cfg.NodeName, hostname())
		out, err := pairclient.Redeem(ctx, h, link, pairclient.Request{Name: name, Platform: runtime.GOOS, AllowOpenRelay: cfg.AllowOpenRelay})
		if err != nil {
			_ = h.Close()
			return nil, fmt.Errorf("redeeming the worker invite: %w", err)
		}
		rec = recordOf(link, out)
		if err := writeJoinRecord(recPath, rec); err != nil {
			_ = h.Close()
			return nil, err
		}
		xlog.Info("Joined the cluster over P2P. Compare this phrase with the one the admin sees before they approve this node",
			"phrase", out.Phrase, "server", link.Peer.String(), "peer", h.ID().String())
	} else {
		xlog.Info("This worker joined this server before; the invite is not redeemed again", "server", link.Peer.String(), "peer", h.ID().String())
	}

	relays, err := p2pnet.RelaysOption(rec.Relays)
	if err != nil {
		_ = h.Close()
		return nil, err
	}
	addrs, err := p2pnet.AddrsOption(rec.Addrs)
	if err != nil {
		_ = h.Close()
		return nil, err
	}
	j.dial = []p2pnet.DialOption{relays, addrs}
	return j, nil
}

func hostname() string {
	name, _ := os.Hostname()
	return name
}

// inviteHash identifies an invite in the record without keeping its code.
func inviteHash(link *p2ppair.Link) string {
	sum := sha256.Sum256([]byte(link.Code))
	return hex.EncodeToString(sum[:16])
}

func recordOf(link *p2ppair.Link, out *pairclient.Result) *joinRecord {
	return &joinRecord{
		Invite:     inviteHash(link),
		ServerPeer: link.Peer.String(), ServerName: out.Server.Name, Kind: out.Kind, DeviceID: out.DeviceID,
		Relays: link.RelayAddrStrings(), Addrs: link.AddrStrings(),
	}
}

func readJoinRecord(path string) (*joinRecord, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is the worker's own join record under its state directory
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the record of the joined server: %w", err)
	}
	var rec joinRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("reading the record of the joined server %s: %w", path, err)
	}
	return &rec, nil
}

func writeJoinRecord(path string, rec *joinRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, b, 0o600)
}

// FrontendURL is the URL that the worker registers with. Its host is the peer
// ID of the frontend; the HTTP client of the join dials it over P2P whatever
// the URL names.
func (j *p2pJoin) FrontendURL() string { return "http://" + j.server.String() }

// HTTPClient returns a client whose requests go to the frontend over P2P.
func (j *p2pJoin) HTTPClient(timeout time.Duration) *http.Client {
	c := j.host.HTTPClient(j.server, j.dial...)
	c.Timeout = timeout
	return c
}

// DialTunnel opens the stream of a lane of the tunnel and runs the hello on it.
// A refusal comes back as a *tunnelDialError with the status of the reply, as a
// refused websocket dial does. The bulk lane needs a path without a data limit.
func (j *p2pJoin) DialTunnel(ctx context.Context, lane tunnel.Lane, nodeID, token string) (net.Conn, error) {
	proto, opts := p2pnet.ProtoTunnelInference, j.dial
	if lane == tunnel.LaneBulk {
		proto = p2pnet.ProtoTunnelBulk
		opts = append(append([]p2pnet.DialOption{}, j.dial...), p2pnet.WithNeed(bulkNeed))
	}
	conn, err := j.host.Dial(ctx, j.server, proto, opts...)
	if err != nil {
		return nil, fmt.Errorf("dialling the worker tunnel over P2P: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(p2pHelloTimeout))
	if err := tunnel.WriteHello(conn, tunnel.Hello{V: tunnel.HelloVersion, NodeID: nodeID, Lane: lane, Token: token}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("dialling the worker tunnel over P2P: %w", err)
	}
	reply, err := tunnel.ReadHelloReply(conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("dialling the worker tunnel over P2P: %w", err)
	}
	if !reply.Accepted() {
		_ = conn.Close()
		return nil, TunnelRefusal(reply.Status, reply.Error)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// tunnelDialer returns the dial of TunnelConfig: nil for a worker that did not
// join over P2P, which dials the websocket.
func (j *p2pJoin) tunnelDialer() func(ctx context.Context, lane tunnel.Lane, nodeID, token string) (net.Conn, error) {
	if j == nil {
		return nil
	}
	return j.DialTunnel
}

// Route reports how the tunnel reaches the frontend now, for the heartbeat.
func (j *p2pJoin) Route() nodes.TunnelRoute {
	p := j.host.Path(j.server)
	if p.Kind == p2pnet.PathNone {
		return nodes.TunnelRoute{Transport: nodes.TunnelP2P}
	}
	return nodes.TunnelRoute{Transport: nodes.TunnelP2P, Path: string(p.Kind), Limited: p.Limited}
}

// Close stops the host.
func (j *p2pJoin) Close() error { return j.host.Close() }
