package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/pkg/logsetup"
	"github.com/mudler/xlog"
)

// P2PIdentityFlags name the identity file of this machine.
type P2PIdentityFlags struct {
	Identity string `env:"LOCALAI_P2P_IDENTITY" type:"path" help:"Identity file (default: <data path>/p2p/identity.key)"`
	DataPath string `env:"LOCALAI_DATA_PATH" type:"path" default:"${basepath}/data" help:"Data path of the server, which holds p2p/identity.key"`
}

func (f P2PIdentityFlags) path() string {
	if f.Identity != "" {
		return f.Identity
	}
	return filepath.Join(f.DataPath, "p2p", "identity.key")
}

// P2PIDCMD prints the peer ID of this machine's identity file.
type P2PIDCMD struct {
	P2PIdentityFlags `embed:""`
	JSON             bool `help:"Print JSON"`
}

func (c *P2PIDCMD) Run(_ *cliContext.Context) error {
	path := c.path()
	id, err := p2pnet.IdentityPeerID(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no identity at %s: start the server with P2P on once, or pass --identity or --data-path", path)
	}
	if err != nil {
		return err
	}
	if c.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"peer_id": id.String(), "identity": path})
	}
	fmt.Println(id)
	return nil
}

// P2PForwardCMD forwards a local port to a peer. The desktop launcher
// supervises it: it keeps one port, re-dials by itself and exits cleanly on
// SIGTERM. While a connection is up it asks the server for its current
// routes and prints them, so the supervisor can save them.
type P2PForwardCMD struct {
	P2PIdentityFlags `embed:""`
	Peer             string   `required:"" help:"Peer ID to forward to"`
	Listen           string   `required:"" help:"Local loopback address to listen on, for example 127.0.0.1:8080"`
	Relay            []string `help:"Relay multiaddr to try, ending in /p2p/<relay ID> (repeatable)"`
	Addr             []string `help:"Known multiaddr of the peer (repeatable)"`
	Hint             []string `help:"Public relay of the invite that only helps a hole punch and never carries data, ending in /p2p/<relay ID> (repeatable)"`
	JSON             bool     `help:"Print one JSON status line per state change on stderr, for a supervisor"`
}

// routesLine is the status line with the routes the server gave.
type routesLine struct {
	State  string   `json:"state"`
	Addrs  []string `json:"addrs"`
	Relays []string `json:"relays"`
	Hints  []string `json:"hints"`
}

// dialOptions are the dial options of the relays, addresses and hints of the
// flags.
func (c *P2PForwardCMD) dialOptions() ([]p2pnet.DialOption, error) {
	var dial []p2pnet.DialOption
	for _, f := range []struct {
		vals []string
		opt  func([]string) (p2pnet.DialOption, error)
	}{{c.Relay, p2pnet.RelaysOption}, {c.Addr, p2pnet.AddrsOption}, {c.Hint, p2pnet.HintsOption}} {
		if len(f.vals) == 0 {
			continue
		}
		o, err := f.opt(f.vals)
		if err != nil {
			return nil, err
		}
		dial = append(dial, o)
	}
	return dial, nil
}

type forwardLine struct {
	State string `json:"state"`
	Addr  string `json:"addr,omitempty"`
	Path  string `json:"path,omitempty"`
	Relay string `json:"relay,omitempty"`
	Error string `json:"error,omitempty"`
}

func (c *P2PForwardCMD) Run(cctx *cliContext.Context) error {
	if c.JSON {
		// A supervisor reads the status lines: logs must not mix with them.
		logsetup.SetLogger(logsetup.NewLogSettings(cctx.Debug, cctx.LogLevel, cctx.LogFormat, cctx.LogDedupLogs), true)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	peerID, err := p2pnet.ParsePeerID(c.Peer)
	if err != nil {
		return err
	}
	dial, err := c.dialOptions()
	if err != nil {
		return err
	}
	identity := c.path()
	if _, err := p2pnet.IdentityPeerID(identity); err != nil {
		return fmt.Errorf("%w; pair this machine first with `local-ai p2p pair --invite <link> --identity %s`", err, identity)
	}
	// Without hints this machine only dials out. With hints it listens on
	// ports that the system picks, so that a hole punch can reach it; its
	// empty allowlist refuses every inbound connection.
	cfg := p2pnet.Config{IdentityPath: identity, ListenAddrs: []string{}}
	if len(c.Hint) > 0 {
		cfg.ListenAddrs = nil
	}
	h, err := p2pnet.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	report := func(l forwardLine) {
		if c.JSON {
			_ = json.NewEncoder(os.Stderr).Encode(l)
			return
		}
		line := "forward " + l.State
		if l.Addr != "" {
			line += " " + l.Addr
		}
		if l.Path != "" {
			line += " (" + l.Path + ")"
		}
		if l.Error != "" {
			line += ": " + l.Error
		}
		_, _ = fmt.Fprintln(os.Stderr, line)
	}
	var mu sync.Mutex
	client := h.HTTPClient(peerID, dial...)
	routes := pairclient.WatchRoutes(ctx,
		func(ctx context.Context) (pairclient.Routes, error) {
			return pairclient.FetchRoutes(ctx, client, peerID)
		},
		0,
		func(r pairclient.Routes) {
			mu.Lock()
			defer mu.Unlock()
			if c.JSON {
				_ = json.NewEncoder(os.Stderr).Encode(routesLine{State: "routes", Addrs: r.Addrs, Relays: r.Relays, Hints: r.Hints})
				return
			}
			_, _ = fmt.Fprintf(os.Stderr, "forward routes: %d addresses, %d relays, %d hints\n", len(r.Addrs), len(r.Relays), len(r.Hints))
		},
		func(err error) { xlog.Debug("p2p forward: could not read the routes of the server", "error", err) })
	defer routes.Stop()
	f, err := h.Forward(ctx, peerID, p2pnet.ProtoHTTP, c.Listen,
		p2pnet.WithDialOptions(dial...),
		p2pnet.WithStatus(func(st p2pnet.ForwardStatus) {
			l := forwardLine{State: string(st.State), Path: string(st.Path.Kind)}
			if st.Path.Relay != "" {
				l.Relay = st.Path.Relay.String()
			}
			if st.Err != nil {
				l.Error = st.Err.Error()
			}
			mu.Lock()
			report(l)
			mu.Unlock()
			routes.Connected(st.State == p2pnet.ForwardUp)
		}))
	if err != nil {
		return err
	}
	mu.Lock()
	report(forwardLine{State: "ready", Addr: f.Addr().String()})
	mu.Unlock()
	<-ctx.Done()
	return f.Close()
}
