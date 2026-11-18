package p2psplit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// Reconnect delays of the control session.
const (
	reconnectMin = time.Second
	reconnectMax = 30 * time.Second
)

// ErrRefused is returned by Agent.Run when the server refused the worker for
// good (wrong engine, not paired). Errors.As gives the *RefusedError.
var ErrRefused = errors.New("p2psplit: the server refused this worker")

// RefusedError carries the reason the server gave.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "the server refused this worker: " + e.Reason }

// Is matches ErrRefused.
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// Assignment is the place of an MLX worker in the ring. Rank 0 means that the
// worker stands by: the MLX process must stop.
type Assignment struct {
	Rank int
	Size int
	// Hostfile is the path of the ring hostfile of this worker: its own
	// entry is the local ring port, every other entry is a local port that
	// reaches that rank through the server.
	Hostfile string
}

// AgentConfig configures an Agent.
type AgentConfig struct {
	Host *p2pnet.Host
	// Server is the peer ID of the server the worker paired with.
	Server p2pnet.ID
	// Dial are the options that reach the server (addresses and relays of
	// the invite).
	Dial   []p2pnet.DialOption
	Engine Engine
	// Name, Version and MemoryBytes are reported to the server.
	Name        string
	Version     string
	MemoryBytes uint64
	// RPCAddr is the address of the local llama.cpp rpc-server (llama).
	RPCAddr string
	// RingDir is where the MLX hostfile is written (mlx).
	RingDir string
	// OnAssign is called when the MLX ring changes (mlx).
	OnAssign func(Assignment)
	// OnConnected is called with true when the server welcomed the worker
	// and with false when the session ended. May be nil.
	OnConnected func(bool)
}

// Agent is the worker side of model splitting. It keeps a control session
// open to the server and serves the data streams the server opens.
type Agent struct {
	cfg AgentConfig

	mu       sync.Mutex
	ringPort int
	rank     int
	size     int
	fwds     []*localForwarder
}

// NewAgent returns an Agent; Run starts it.
func NewAgent(cfg AgentConfig) (*Agent, error) {
	if cfg.Host == nil || cfg.Server == "" {
		return nil, errors.New("p2psplit: host and server are required")
	}
	switch cfg.Engine {
	case EngineLlama:
		if cfg.RPCAddr == "" {
			return nil, errors.New("p2psplit: the llama.cpp worker needs the rpc-server address")
		}
	case EngineMLX:
		if cfg.RingDir == "" || cfg.OnAssign == nil {
			return nil, errors.New("p2psplit: the MLX worker needs a ring directory and OnAssign")
		}
	default:
		return nil, fmt.Errorf("p2psplit: unknown engine %q", cfg.Engine)
	}
	return &Agent{cfg: cfg}, nil
}

// Run serves until ctx ends or the server refuses the worker. It reconnects
// the control session after a failure.
func (a *Agent) Run(ctx context.Context) error {
	admit := func(p p2pnet.ID) bool { return p == a.cfg.Server }
	var wg sync.WaitGroup
	var l net.Listener
	var err error
	switch a.cfg.Engine {
	case EngineLlama:
		l, err = a.cfg.Host.Listen(ProtoLlamaRPC, admit)
		if err != nil {
			return err
		}
		serveListener(&wg, l, func(c net.Conn) { pipeTCP(ctx, c, a.cfg.RPCAddr) })
	case EngineMLX:
		p, err := freePort()
		if err != nil {
			return err
		}
		a.ringPort = p
		l, err = a.cfg.Host.Listen(ProtoMLX, admit)
		if err != nil {
			return err
		}
		serveListener(&wg, l, func(c net.Conn) { a.serveRing(ctx, c) })
	}
	defer func() {
		_ = l.Close()
		a.mu.Lock()
		a.closeRingLocked()
		a.mu.Unlock()
		wg.Wait()
	}()

	delay := reconnectMin
	for {
		welcomed, err := a.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrRefused) {
			return err
		}
		if welcomed {
			delay = reconnectMin
		}
		xlog.Warn("Connection to the server lost, retrying", "in", delay.String(), "error", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, reconnectMax)
	}
}

// session runs one control session. welcomed reports whether the server
// accepted the worker before the session ended.
func (a *Agent) session(ctx context.Context) (welcomed bool, err error) {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	c, err := a.cfg.Host.Dial(dctx, a.cfg.Server, ProtoControl, a.cfg.Dial...)
	cancel()
	if err != nil {
		return false, err
	}
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	var wmu sync.Mutex
	send := func(m message) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return writeMsg(c, m)
	}
	if err := send(message{Type: msgHello, V: protoVersion, Engine: a.cfg.Engine, Name: a.cfg.Name, Version: a.cfg.Version, MemoryBytes: a.cfg.MemoryBytes}); err != nil {
		return false, err
	}
	_ = c.SetReadDeadline(time.Now().Add(dialTimeout))
	var first message
	if err := readMsg(c, &first); err != nil {
		return false, err
	}
	_ = c.SetReadDeadline(time.Time{})
	switch first.Type {
	case msgWelcome:
	case msgRefused:
		return false, &RefusedError{Reason: first.Err}
	default:
		return false, fmt.Errorf("p2psplit: unexpected message %q", first.Type)
	}
	path := a.cfg.Host.Path(a.cfg.Server)
	xlog.Info("Connected to the server", "server", a.cfg.Server.String(), "path", string(path.Kind))
	if a.cfg.OnConnected != nil {
		a.cfg.OnConnected(true)
		defer a.cfg.OnConnected(false)
	}

	hb := make(chan struct{})
	go func() {
		defer close(hb)
		t := time.NewTicker(heartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := send(message{Type: msgHeartbeat}); err != nil {
					_ = c.Close()
					return
				}
			}
		}
	}()
	defer func() { <-hb }()
	for {
		var m message
		if err := readMsg(c, &m); err != nil {
			_ = c.Close()
			return true, err
		}
		if m.Type == msgAssign && a.cfg.Engine == EngineMLX {
			a.assign(ctx, m.Rank, m.Size)
		}
	}
}

// assign sets up the ring forwarders and hostfile for a new rank and tells
// the caller.
func (a *Agent) assign(ctx context.Context, rank, size int) {
	a.mu.Lock()
	if rank == a.rank && size == a.size {
		a.mu.Unlock()
		return
	}
	a.closeRingLocked()
	a.rank, a.size = rank, size
	if rank <= 0 || size < 2 || rank >= size {
		a.rank, a.size = 0, 0
		a.mu.Unlock()
		xlog.Info("The server put this worker on standby")
		a.cfg.OnAssign(Assignment{})
		return
	}
	entries := make([]string, size)
	for j := range size {
		if j == rank {
			entries[j] = net.JoinHostPort(loopback, strconv.Itoa(a.ringPort))
			continue
		}
		target := j
		f, err := newLocalForwarder(ctx, 0, func(ctx context.Context) (net.Conn, error) {
			return dialRing(ctx, a.cfg.Host, a.cfg.Server, target, append(a.cfg.Dial, p2pnet.WithNeed(p2pnet.NeedBulk))...)
		})
		if err != nil {
			a.closeRingLocked()
			a.rank, a.size = 0, 0
			a.mu.Unlock()
			xlog.Error("Could not open the MLX ring ports", "error", err)
			a.cfg.OnAssign(Assignment{})
			return
		}
		a.fwds = append(a.fwds, f)
		entries[j] = f.addr()
	}
	a.mu.Unlock()
	path, err := writeHostfile(a.cfg.RingDir, entries)
	if err != nil {
		xlog.Error("Could not write the MLX hostfile", "error", err)
		return
	}
	xlog.Info("The server gave this worker its place in the MLX ring", "rank", rank, "size", size)
	a.cfg.OnAssign(Assignment{Rank: rank, Size: size, Hostfile: path})
}

func (a *Agent) closeRingLocked() {
	for _, f := range a.fwds {
		f.close()
	}
	a.fwds = nil
}

// serveRing takes a ring stream from the server and connects it to the local
// MLX process when the header names this worker's rank.
func (a *Agent) serveRing(ctx context.Context, c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(dialTimeout))
	var h ringHeader
	if err := readMsg(c, &h); err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	a.mu.Lock()
	rank, port := a.rank, a.ringPort
	a.mu.Unlock()
	if rank == 0 || h.Rank != rank {
		_ = c.Close()
		return
	}
	pipeTCP(ctx, c, net.JoinHostPort(loopback, strconv.Itoa(port)))
}

// writeHostfile writes the ring hostfile as a JSON array of "host:port".
func writeHostfile(dir string, entries []string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "mlx-hostfile.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}
