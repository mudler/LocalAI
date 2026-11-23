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
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/pkg/logsetup"
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
// SIGTERM.
type P2PForwardCMD struct {
	P2PIdentityFlags `embed:""`
	Peer             string   `required:"" help:"Peer ID to forward to"`
	Listen           string   `required:"" help:"Local loopback address to listen on, for example 127.0.0.1:8080"`
	Relay            []string `help:"Relay multiaddr to try, ending in /p2p/<relay ID> (repeatable)"`
	Addr             []string `help:"Known multiaddr of the peer (repeatable)"`
	JSON             bool     `help:"Print one JSON status line per state change on stderr, for a supervisor"`
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
	var dial []p2pnet.DialOption
	if len(c.Relay) > 0 {
		o, err := p2pnet.RelaysOption(c.Relay)
		if err != nil {
			return err
		}
		dial = append(dial, o)
	}
	if len(c.Addr) > 0 {
		o, err := p2pnet.AddrsOption(c.Addr)
		if err != nil {
			return err
		}
		dial = append(dial, o)
	}
	identity := c.path()
	if _, err := p2pnet.IdentityPeerID(identity); err != nil {
		return fmt.Errorf("%w; pair this machine first with `local-ai p2p pair --invite <link> --identity %s`", err, identity)
	}
	// No listener: this machine only dials out.
	h, err := p2pnet.New(ctx, p2pnet.Config{IdentityPath: identity, ListenAddrs: []string{}})
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
			report(l)
		}))
	if err != nil {
		return err
	}
	report(forwardLine{State: "ready", Addr: f.Addr().String()})
	<-ctx.Done()
	return f.Close()
}
