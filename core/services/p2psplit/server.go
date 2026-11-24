package p2psplit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// evaluateEvery is how often the manager checks the paths of the workers
// when no event arrives.
const evaluateEvery = 5 * time.Second

// rank0Key is the PortBook key of the MLX rank-0 ring port of the server.
const rank0Key = "mlx-rank0"

// State is how the server uses a split worker.
type State string

// The states of a split worker.
const (
	// StateOffline: the worker has no control session.
	StateOffline State = "offline"
	// StateOnline: the worker is in use.
	StateOnline State = "online"
	// StateSlow: the only path is a relay. The worker waits until the
	// administrator chooses "Use anyway" (AllowSlowPath).
	StateSlow State = "slow"
	// StateLimited: the only path is a relay whose limits are too small to
	// carry model layers. No choice makes it usable; a direct path or a
	// relay without a data limit does.
	StateLimited State = "limited"
	// StateRefused: the worker connected with another engine than the
	// invite named.
	StateRefused State = "refused"
)

// Device is a paired split worker as the store knows it.
type Device struct {
	ID    string
	Label string
	Peer  p2pnet.ID
	// Engine is the engine the invite named.
	Engine Engine
	// AllowSlowPath is the "Use anyway" choice.
	AllowSlowPath bool
	CreatedAt     time.Time
}

// Worker is the state of one split worker.
type Worker struct {
	DeviceID      string
	Label         string
	Peer          string
	Engine        Engine
	AllowSlowPath bool
	State         State
	// Path is the best connection to the worker; Kind is none when offline.
	Path p2pnet.Path
	// OwnRelay is true when Path goes through a relay the administrator
	// controls, whose limits can change.
	OwnRelay bool
	// MemoryBytes is the memory the worker reported (VRAM, else RAM).
	MemoryBytes uint64
	// Name and Version are what the worker reported about itself.
	Name    string
	Version string
	// Addr is the loopback address that reaches the worker: the llama.cpp
	// RPC server, or the MLX ring port. Empty unless online.
	Addr string
	// Rank is the MLX rank; zero when not in the ring.
	Rank int
	// Since is when the control session opened.
	Since *time.Time
	// Error says why the worker is refused.
	Error string
}

// Config configures a Manager.
type Config struct {
	Host *p2pnet.Host
	// Devices lists the active split devices.
	Devices func(ctx context.Context) ([]Device, error)
	// OnLlama receives the addresses of the llama.cpp RPC servers in use,
	// each time the set changes; nil or empty means none.
	OnLlama func(addrs []string)
	// OnMLX receives the MLX ring hostfile of the server (rank 0 first),
	// each time it changes; empty means no ring.
	OnMLX func(hostfile []string)
	// OwnRelay reports whether a relay is one the administrator controls
	// (a gated relay of this server), whose limits can change. A slow worker
	// behind such a relay may be used even when the relay limits each
	// connection. Nil means none.
	OwnRelay func(relay p2pnet.ID) bool
	// Ports keeps the loopback ports across managers, so a model loaded
	// with an address keeps working when P2P restarts. Nil keeps none.
	Ports *PortBook
	// Elsewhere answers the hello of a peer that is not one of Devices. It
	// returns false when the peer is no split worker, which refuses it for
	// good. A frontend answers with the backend node a worker is attached
	// to; a node answers Wait for a worker it does not have yet. Nil
	// refuses every unknown peer.
	Elsewhere func(ctx context.Context, p p2pnet.ID) (Move, bool)
}

// Move tells a worker where its control session belongs.
type Move struct {
	// Peer and Addrs reach the backend node that serves the worker.
	Peer  p2pnet.ID
	Addrs []string
	// Wait, when Peer is empty, says why the worker must ask again later.
	Wait string
}

// Manager runs the server side of model splitting: it accepts the control
// sessions of the paired split workers, forwards a stable loopback port to
// each one that can carry model traffic, and passes the addresses to the
// engines.
//
// It keeps state of this server only: in a cluster each frontend runs its own
// manager on its own host; the devices are shared through the store.
type Manager struct {
	cfg Config

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	kick   chan struct{}

	mu      sync.Mutex
	devices map[p2pnet.ID]Device
	workers map[string]*worker // by device ID
	llama   []string
	mlx     []string
	ctl     net.Listener
	ring    net.Listener

	// conns are the accepted connections that are open, so that Close can
	// end the ones that wait for a hello or carry a ring stream. connMu
	// guards them and closed; it is taken alone or last.
	connMu sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

// worker is the live state of one device.
type worker struct {
	sess     *session
	hello    message
	refusal  string
	state    State
	path     p2pnet.Path
	fwd      *p2pnet.Forwarder
	fwdNeed  p2pnet.Need
	own      bool
	ringFwd  *localForwarder
	rank     int
	assigned assignment // last sent
}

// assignment is the place of an MLX worker in the ring as last sent.
type assignment struct {
	rank, size int
	anyway     bool
}

// session is one open control session.
type session struct {
	conn  net.Conn
	since time.Time
	wmu   sync.Mutex
}

func (s *session) send(m message) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return writeMsg(s.conn, m)
}

// NewManager starts a manager on cfg.Host. Close stops it.
func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	if cfg.Host == nil || cfg.Devices == nil {
		return nil, errors.New("p2psplit: host and devices are required")
	}
	if cfg.Ports == nil {
		cfg.Ports = NewPortBook()
	}
	m := &Manager{cfg: cfg, kick: make(chan struct{}, 1), devices: map[p2pnet.ID]Device{}, workers: map[string]*worker{}, conns: map[net.Conn]struct{}{}}
	m.ctx, m.cancel = context.WithCancel(ctx)
	ctl, err := cfg.Host.Listen(ProtoControl, nil)
	if err != nil {
		m.cancel()
		return nil, err
	}
	ring, err := cfg.Host.Listen(ProtoMLX, m.inRing)
	if err != nil {
		_ = ctl.Close()
		m.cancel()
		return nil, err
	}
	m.ctl, m.ring = ctl, ring
	m.Refresh()
	serveListener(&m.wg, ctl, m.tracked(m.serveControl))
	serveListener(&m.wg, ring, m.tracked(m.serveRing))
	m.wg.Go(m.loop)
	return m, nil
}

// Close stops the manager, closes every forwarder and session, and tells the
// engines that no worker is left.
func (m *Manager) Close() error {
	m.cancel()
	_ = m.ctl.Close()
	_ = m.ring.Close()
	m.closeConns()
	var closers []func()
	m.mu.Lock()
	for _, w := range m.workers {
		if w.sess != nil {
			_ = w.sess.conn.Close()
		}
		m.detachLocked(w, &closers)
	}
	hadLlama, hadMLX := len(m.llama) > 0, len(m.mlx) > 0
	m.llama, m.mlx = nil, nil
	m.mu.Unlock()
	runAll(closers)
	m.wg.Wait()
	if hadLlama && m.cfg.OnLlama != nil {
		m.cfg.OnLlama(nil)
	}
	if hadMLX && m.cfg.OnMLX != nil {
		m.cfg.OnMLX(nil)
	}
	return nil
}

// tracked wraps a connection handler so that Close can end its connection.
func (m *Manager) tracked(fn func(net.Conn)) func(net.Conn) {
	return func(c net.Conn) {
		m.connMu.Lock()
		if m.closed {
			m.connMu.Unlock()
			_ = c.Close()
			return
		}
		m.conns[c] = struct{}{}
		m.connMu.Unlock()
		defer func() {
			m.connMu.Lock()
			delete(m.conns, c)
			m.connMu.Unlock()
		}()
		fn(c)
	}
}

// closeConns closes every open accepted connection and refuses new ones. A
// handler that waits for a hello or pipes a ring stream does not watch the
// context, so without this Close would wait for it in m.wg.Wait.
func (m *Manager) closeConns() {
	m.connMu.Lock()
	m.closed = true
	conns := make([]net.Conn, 0, len(m.conns))
	for c := range m.conns {
		conns = append(conns, c)
	}
	m.connMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// Refresh reloads the devices from the store and drops the sessions of the
// devices that are gone (revoked).
func (m *Manager) Refresh() {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()
	devs, err := m.cfg.Devices(ctx)
	if err != nil {
		xlog.Warn("p2p split: could not load the split workers", "error", err)
		return
	}
	next := make(map[p2pnet.ID]Device, len(devs))
	ids := make(map[string]bool, len(devs))
	for _, d := range devs {
		next[d.Peer] = d
		ids[d.ID] = true
	}
	var closers []func()
	m.mu.Lock()
	m.devices = next
	for id, w := range m.workers {
		if ids[id] {
			continue
		}
		if w.sess != nil {
			_ = w.sess.conn.Close()
		}
		m.detachLocked(w, &closers)
		delete(m.workers, id)
	}
	m.mu.Unlock()
	runAll(closers)
	m.Kick()
}

// Kick asks for a new evaluation now, for example after a setting changed.
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Workers returns the state of every active split device, ordered by the
// time it was paired.
func (m *Manager) Workers() []Worker {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Worker, 0, len(m.devices))
	for _, d := range m.orderedLocked() {
		v := Worker{
			DeviceID: d.ID, Label: d.Label, Peer: d.Peer.String(), Engine: d.Engine,
			AllowSlowPath: d.AllowSlowPath, State: StateOffline, Path: p2pnet.Path{Kind: p2pnet.PathNone},
		}
		if w := m.workers[d.ID]; w != nil {
			v.State, v.Error, v.Rank, v.OwnRelay = w.state, w.refusal, w.rank, w.own
			if w.sess != nil {
				since := w.sess.since
				v.Since = &since
				v.Path = w.path
				v.MemoryBytes, v.Name, v.Version = w.hello.MemoryBytes, w.hello.Name, w.hello.Version
				if w.hello.Engine != "" {
					v.Engine = w.hello.Engine
				}
			}
			switch {
			case w.fwd != nil:
				v.Addr = w.fwd.Addr().String()
			case w.ringFwd != nil:
				v.Addr = w.ringFwd.addr()
			}
		}
		out = append(out, v)
	}
	return out
}

// orderedLocked returns the devices by pairing time, then ID.
func (m *Manager) orderedLocked() []Device {
	out := make([]Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Device) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}

func (m *Manager) loop() {
	paths := m.cfg.Host.WatchPaths(m.ctx)
	t := time.NewTicker(evaluateEvery)
	defer t.Stop()
	for {
		m.evaluate()
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		case <-m.kick:
		case _, ok := <-paths:
			if !ok {
				paths = nil
			}
		}
	}
}

// evaluate decides the state of each worker from its session and path,
// starts and stops the forwarders, sends the MLX ranks and calls the engine
// callbacks when the sets change.
func (m *Manager) evaluate() {
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	var llama []string
	var mlxReady []string
	var closers []func()
	defer func() { runAll(closers) }()
	for _, d := range m.orderedLocked() {
		w := m.workers[d.ID]
		if w == nil {
			continue
		}
		m.decideLocked(d, w)
		if w.state != StateOnline {
			m.detachLocked(w, &closers)
			continue
		}
		switch w.hello.Engine {
		case EngineLlama:
			if addr := m.llamaForwarderLocked(d, w); addr != "" {
				llama = append(llama, addr)
			}
		case EngineMLX:
			mlxReady = append(mlxReady, d.ID)
		}
	}
	hostfile := m.ringLocked(mlxReady)
	var sends []func()
	for _, d := range m.orderedLocked() {
		w := m.workers[d.ID]
		if w == nil || w.sess == nil || w.hello.Engine != EngineMLX {
			continue
		}
		want := assignment{}
		if w.rank > 0 {
			want = assignment{rank: w.rank, size: len(hostfile), anyway: needFor(w.own, d.AllowSlowPath) == anywayNeed}
		}
		if want == w.assigned {
			continue
		}
		w.assigned = want
		sess := w.sess
		sends = append(sends, func() {
			if err := sess.send(message{Type: msgAssign, V: protoVersion, Rank: want.rank, Size: want.size, Anyway: want.anyway}); err != nil {
				_ = sess.conn.Close()
			}
		})
	}
	var onLlama, onMLX func()
	if !slices.Equal(llama, m.llama) {
		m.llama = llama
		if cb := m.cfg.OnLlama; cb != nil {
			addrs := slices.Clone(llama)
			onLlama = func() { cb(addrs) }
		}
		xlog.Info("p2p split: llama.cpp RPC workers in use", "addrs", llama)
	}
	if !slices.Equal(hostfile, m.mlx) {
		m.mlx = hostfile
		if cb := m.cfg.OnMLX; cb != nil {
			hf := slices.Clone(hostfile)
			onMLX = func() { cb(hf) }
		}
		xlog.Info("p2p split: MLX ring", "hostfile", hostfile)
	}
	m.mu.Unlock()
	for _, f := range sends {
		f()
	}
	if onLlama != nil {
		onLlama()
	}
	if onMLX != nil {
		onMLX()
	}
}

// anywayNeed is what model traffic needs from a relay of the user after
// "Use anyway": more data per connection than a public relay allows (128 KiB),
// so a public relay never carries model layers. The connection still ends at
// the relay's own limits; the administrator accepted that.
var anywayNeed = p2pnet.Need{Bytes: 256 << 10}

// needFor is the need of the model traffic of a worker: NeedBulk (a direct
// path or a relay without a data limit), or anywayNeed once the
// administrator chose "Use anyway" for a path through a relay of theirs.
func needFor(own, allowSlow bool) p2pnet.Need {
	if own && allowSlow {
		return anywayNeed
	}
	return p2pnet.NeedBulk
}

// ownRelay reports whether the relay of path is one the administrator
// controls (a gated relay of this server, or this server), so its limits
// can change.
func (m *Manager) ownRelay(path p2pnet.Path) bool {
	if path.Kind != p2pnet.PathRelayed || path.Relay == "" {
		return false
	}
	if path.Relay == m.cfg.Host.ID() {
		return true
	}
	return m.cfg.OwnRelay != nil && m.cfg.OwnRelay(path.Relay)
}

// decideLocked sets w.state, w.path and w.own.
func (m *Manager) decideLocked(d Device, w *worker) {
	w.path = p2pnet.Path{Kind: p2pnet.PathNone}
	if w.sess != nil {
		w.path = m.cfg.Host.Path(d.Peer)
	}
	w.own = m.ownRelay(w.path)
	w.state = decide(w.sess != nil, w.refusal, w.path, w.own, d.AllowSlowPath)
}

// decide is the policy of the slow path. A direct or hole-punched path is
// used. A relayed path is slow: it is used after the administrator chose
// "Use anyway", when it can carry model layers at all. A relay with a data
// limit qualifies only when it is the administrator's own (its limits can
// change); a public relay never does.
func decide(connected bool, refusal string, path p2pnet.Path, own, allowSlow bool) State {
	switch {
	case refusal != "":
		return StateRefused
	case !connected || path.Kind == p2pnet.PathNone:
		return StateOffline
	case path.Kind != p2pnet.PathRelayed:
		return StateOnline
	case !p2pnet.NeedBulk.SatisfiedBy(path) && !(own && anywayNeed.SatisfiedBy(path)):
		return StateLimited
	case !allowSlow:
		return StateSlow
	}
	return StateOnline
}

// llamaForwarderLocked starts the forwarder of an online llama.cpp worker
// and returns its address. The forwarder dials with the need of the worker
// (see needFor), so a public relay never carries model layers even when the
// path changes after this check. A forwarder whose need changed is replaced
// on the same port.
func (m *Manager) llamaForwarderLocked(d Device, w *worker) string {
	need := needFor(w.own, d.AllowSlowPath)
	if w.fwd != nil && w.fwdNeed != need {
		// Closed at once and not after the unlock: the new forwarder binds the
		// same port below. Closing a p2pnet.Forwarder never takes m.mu.
		_ = w.fwd.Close()
		w.fwd = nil
	}
	if w.fwd == nil {
		f, err := m.forward(d, m.cfg.Ports.Get(d.ID), need)
		if err != nil {
			xlog.Warn("p2p split: the port of a worker is in use, taking another one", "worker", d.Label, "error", err)
			f, err = m.forward(d, 0, need)
		}
		if err != nil {
			xlog.Warn("p2p split: could not forward a worker", "worker", d.Label, "error", err)
			return ""
		}
		w.fwd, w.fwdNeed = f, need
		if a, ok := f.Addr().(*net.TCPAddr); ok {
			m.cfg.Ports.Set(d.ID, a.Port)
		}
	}
	return w.fwd.Addr().String()
}

func (m *Manager) forward(d Device, port int, need p2pnet.Need) (*p2pnet.Forwarder, error) {
	return m.cfg.Host.Forward(m.ctx, d.Peer, ProtoLlamaRPC, net.JoinHostPort(loopback, strconv.Itoa(port)),
		p2pnet.WithDialOptions(p2pnet.WithNeed(need)))
}

// ringLocked assigns the MLX ranks in pairing order, keeps a ring forwarder
// per ranked worker, and returns the hostfile of rank 0.
func (m *Manager) ringLocked(ready []string) []string {
	for _, w := range m.workers {
		if w.hello.Engine == EngineMLX && !slices.Contains(ready, m.idOfLocked(w)) {
			w.rank = 0
		}
	}
	if len(ready) == 0 {
		return nil
	}
	rank0 := m.cfg.Ports.Get(rank0Key)
	if rank0 == 0 {
		p, err := freePort()
		if err != nil {
			xlog.Warn("p2p split: no port for the MLX ring", "error", err)
			return nil
		}
		rank0 = p
		m.cfg.Ports.Set(rank0Key, p)
	}
	hostfile := []string{net.JoinHostPort(loopback, strconv.Itoa(rank0))}
	for i, id := range ready {
		w := m.workers[id]
		w.rank = i + 1
		if w.ringFwd == nil {
			d := m.deviceByIDLocked(id)
			f, err := newLocalForwarder(m.ctx, m.cfg.Ports.Get(id), func(ctx context.Context) (net.Conn, error) {
				return m.dialWorkerRing(ctx, id)
			})
			if err != nil {
				f, err = newLocalForwarder(m.ctx, 0, func(ctx context.Context) (net.Conn, error) {
					return m.dialWorkerRing(ctx, id)
				})
			}
			if err != nil {
				xlog.Warn("p2p split: could not forward an MLX worker", "worker", d.Label, "error", err)
				return nil
			}
			w.ringFwd = f
			m.cfg.Ports.Set(id, f.port())
		}
		hostfile = append(hostfile, w.ringFwd.addr())
	}
	return hostfile
}

func (m *Manager) idOfLocked(target *worker) string {
	for id, w := range m.workers {
		if w == target {
			return id
		}
	}
	return ""
}

func (m *Manager) deviceByIDLocked(id string) Device {
	for _, d := range m.devices {
		if d.ID == id {
			return d
		}
	}
	return Device{}
}

// dialWorkerRing opens a ring stream to the worker of device id, for its
// current rank.
func (m *Manager) dialWorkerRing(ctx context.Context, id string) (net.Conn, error) {
	m.mu.Lock()
	d := m.deviceByIDLocked(id)
	w := m.workers[id]
	rank, need := 0, p2pnet.NeedBulk
	if w != nil {
		rank, need = w.rank, needFor(w.own, d.AllowSlowPath)
	}
	m.mu.Unlock()
	if rank == 0 || d.Peer == "" {
		return nil, fmt.Errorf("p2psplit: worker %s has no rank", id)
	}
	return dialRing(ctx, m.cfg.Host, d.Peer, rank, p2pnet.WithNeed(need))
}

// detachLocked takes the forwarders off w and adds their close to *closers.
// The caller closes them after it unlocks: a ring forwarder that is dialing
// waits for m.mu, so closing it under the lock would deadlock.
func (m *Manager) detachLocked(w *worker, closers *[]func()) {
	if f := w.fwd; f != nil {
		*closers = append(*closers, func() { _ = f.Close() })
		w.fwd = nil
	}
	if f := w.ringFwd; f != nil {
		*closers = append(*closers, f.close)
		w.ringFwd = nil
	}
}

func runAll(fns []func()) {
	for _, f := range fns {
		f()
	}
}

// device returns the split device of p, reloading the store once when p is
// not known (it may have just paired).
func (m *Manager) device(p p2pnet.ID) (Device, bool) {
	m.mu.Lock()
	d, ok := m.devices[p]
	m.mu.Unlock()
	if ok {
		return d, true
	}
	m.Refresh()
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok = m.devices[p]
	return d, ok
}

func (m *Manager) serveControl(c net.Conn) {
	defer func() { _ = c.Close() }()
	p, ok := p2pnet.PeerOf(c)
	if !ok {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(sessionTimeout))
	var hello message
	if err := readMsg(c, &hello); err != nil || hello.Type != msgHello {
		return
	}
	d, ok := m.device(p)
	if !ok {
		m.elsewhere(c, p)
		return
	}
	refusal := ""
	switch {
	case hello.V != protoVersion:
		refusal = fmt.Sprintf("protocol version %d is not supported; update the worker", hello.V)
	case !hello.Engine.Valid():
		refusal = fmt.Sprintf("unknown engine %q", hello.Engine)
	case d.Engine != "" && hello.Engine != d.Engine:
		refusal = fmt.Sprintf("the invite was made for %s, but this worker runs %s: start the worker with the matching command or make a new invite", engineName(d.Engine), engineName(hello.Engine))
	}
	sess := &session{conn: c, since: time.Now()}
	m.mu.Lock()
	w := m.workers[d.ID]
	if w == nil {
		w = &worker{}
		m.workers[d.ID] = w
	}
	if w.sess != nil {
		// A new session replaces the old one: the worker restarted.
		_ = w.sess.conn.Close()
	}
	w.refusal = refusal
	w.sess, w.hello, w.assigned = sess, hello, assignment{}
	if refusal != "" {
		w.sess = nil
	}
	m.mu.Unlock()
	if refusal != "" {
		xlog.Warn("p2p split: refused a worker", "worker", d.Label, "reason", refusal)
		_ = sess.send(message{Type: msgRefused, V: protoVersion, Err: refusal})
		m.Kick()
		return
	}
	if err := sess.send(message{Type: msgWelcome, V: protoVersion}); err != nil {
		m.dropSession(d.ID, sess)
		return
	}
	xlog.Info("p2p split: worker connected", "worker", d.Label, "engine", string(hello.Engine), "peer", p.String())
	m.Kick()
	for {
		_ = c.SetReadDeadline(time.Now().Add(sessionTimeout))
		var msg message
		if err := readMsg(c, &msg); err != nil {
			break
		}
	}
	m.dropSession(d.ID, sess)
	xlog.Info("p2p split: worker disconnected", "worker", d.Label)
}

// elsewhere answers a peer that is not one of the devices: moved, wait, or
// refused when it is no split worker at all.
func (m *Manager) elsewhere(c net.Conn, p p2pnet.ID) {
	var mv Move
	ok := false
	if m.cfg.Elsewhere != nil {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		mv, ok = m.cfg.Elsewhere(ctx, p)
		cancel()
	}
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	switch {
	case !ok:
		_ = writeMsg(c, message{Type: msgRefused, V: protoVersion, Err: "this peer is not paired as a model-split worker: redeem a split invite first"})
	case mv.Peer != "":
		xlog.Debug("p2p split: sent a worker to its node", "worker", p.String(), "node", mv.Peer.String())
		_ = writeMsg(c, message{Type: msgMoved, V: protoVersion, Peer: mv.Peer.String(), Addrs: mv.Addrs})
	default:
		_ = writeMsg(c, message{Type: msgWait, V: protoVersion, Err: mv.Wait})
	}
}

func (m *Manager) dropSession(id string, sess *session) {
	var closers []func()
	m.mu.Lock()
	if w := m.workers[id]; w != nil && w.sess == sess {
		w.sess = nil
		m.detachLocked(w, &closers)
	}
	m.mu.Unlock()
	runAll(closers)
	m.Kick()
}

// inRing admits ring streams from MLX workers that hold a rank.
func (m *Manager) inRing(p p2pnet.ID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[p]
	if !ok {
		return false
	}
	w := m.workers[d.ID]
	return w != nil && w.rank > 0
}

// serveRing relays a ring stream of a worker to the rank its header names:
// rank 0 is the MLX process of this server, any other rank is another
// worker.
func (m *Manager) serveRing(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(dialTimeout))
	var h ringHeader
	if err := readMsg(c, &h); err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	if h.Rank == 0 {
		m.mu.Lock()
		port := m.cfg.Ports.Get(rank0Key)
		inRing := len(m.mlx) > 0
		m.mu.Unlock()
		if !inRing || port == 0 {
			_ = c.Close()
			return
		}
		pipeTCP(m.ctx, c, net.JoinHostPort(loopback, strconv.Itoa(port)))
		return
	}
	m.mu.Lock()
	var target string
	for id, w := range m.workers {
		if w.rank == h.Rank {
			target = id
		}
	}
	m.mu.Unlock()
	if target == "" {
		_ = c.Close()
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, dialTimeout)
	remote, err := m.dialWorkerRing(ctx, target)
	cancel()
	if err != nil {
		xlog.Debug("p2p split: could not relay a ring stream", "rank", h.Rank, "error", err)
		_ = c.Close()
		return
	}
	if err := p2pnet.Pipe(c, remote); err != nil {
		xlog.Debug("p2p split: relayed ring stream ended", "error", err)
	}
}

func engineName(e Engine) string {
	switch e {
	case EngineLlama:
		return "llama.cpp RPC"
	case EngineMLX:
		return "MLX"
	}
	return string(e)
}

// PortBook remembers the loopback port of each worker, so that a worker
// keeps its address while the server runs, even across P2P restarts. Engines
// that loaded a model keep the addresses they read at load time.
type PortBook struct {
	mu    sync.Mutex
	ports map[string]int
}

// NewPortBook returns an empty PortBook.
func NewPortBook() *PortBook { return &PortBook{ports: map[string]int{}} }

// Get returns the port of key, zero when none.
func (b *PortBook) Get(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ports[key]
}

// Set records the port of key.
func (b *PortBook) Set(key string, port int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ports[key] = port
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(loopback, "0"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
