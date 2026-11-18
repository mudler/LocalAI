// Package p2prelaymgr keeps the relays a LocalAI server uses. For each relay
// it connects, reads the admin status, pushes the server's paired set and
// keeps it in sync: a full push on every connect, an add or remove when a
// device is paired or revoked, and a digest comparison every minute. It also
// registers and cancels the one-time relay passes of invites, and checks a
// relay step by step for the interface.
package p2prelaymgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// Defaults of Config.
const (
	DefaultTick         = 15 * time.Second
	DefaultSyncInterval = time.Minute
	DefaultRetry        = 5 * time.Second
	connectTimeout      = 10 * time.Second
)

// Config configures New.
type Config struct {
	// Host is the server's P2P host. Required.
	Host *p2pnet.Host
	// Store persists the relay list. Nil means an in-memory list.
	Store Store
	// PairedPeers returns the peer IDs of the paired devices. Required.
	PairedPeers func() []string
	// Logger receives the manager's log lines. Nil means slog.Default().
	Logger *slog.Logger
	// Tick is how often the loop looks for a lost connection. Zero means
	// DefaultTick.
	Tick time.Duration
	// SyncInterval is how often the loop compares digests. Zero means
	// DefaultSyncInterval.
	SyncInterval time.Duration
	// Retry is how long a queued change waits before it is sent again after a
	// failure. Zero means DefaultRetry.
	Retry time.Duration
}

type setOp struct {
	id   uint64
	op   string
	peer string
}

type entry struct {
	// opMu serialises the operations that change or read the set of the
	// relay, so their sequence numbers go out in order.
	opMu sync.Mutex

	mu        sync.Mutex
	relay     Relay
	info      peer.AddrInfo
	queue     []setOp
	lastCheck time.Time
	removed   bool

	kick chan struct{}
}

type manager struct {
	cfg Config
	log *slog.Logger

	mu      sync.Mutex
	relays  []*entry
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	lastSeq  atomic.Int64
	lastOpID atomic.Uint64
}

// New returns a Manager. It loads the relay list from the store; nothing
// touches the network until Start, Add or another call needs it.
func New(cfg Config) (Manager, error) {
	if cfg.Host == nil {
		return nil, errors.New("p2prelaymgr: Host is required")
	}
	if cfg.PairedPeers == nil {
		return nil, errors.New("p2prelaymgr: PairedPeers is required")
	}
	if cfg.Store == nil {
		cfg.Store = NewMemoryStore()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Tick <= 0 {
		cfg.Tick = DefaultTick
	}
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = DefaultSyncInterval
	}
	if cfg.Retry <= 0 {
		cfg.Retry = DefaultRetry
	}
	m := &manager{cfg: cfg, log: cfg.Logger}
	stored, err := cfg.Store.Load(context.Background())
	if err != nil {
		return nil, err
	}
	for _, s := range stored {
		e, err := newEntry(s)
		if err != nil {
			m.log.Warn("ignoring a stored relay with a bad address", "relay", s.ID, "error", err)
			continue
		}
		m.relays = append(m.relays, e)
	}
	return m, nil
}

func newEntry(s Entry) (*entry, error) {
	ai, err := peer.AddrInfoFromString(s.Addr)
	if err != nil {
		return nil, ErrBadAddr
	}
	return &entry{
		info: *ai,
		kick: make(chan struct{}, 1),
		relay: Relay{
			ID: ai.ID.String(), Label: s.Label, Addr: s.Addr,
			Mode: ModeUnknown, State: StateChecking, Health: "not checked yet",
		},
	}, nil
}

func (m *manager) lh() host.Host { return m.cfg.Host.Libp2p() }

func (e *entry) snapshot() Relay {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.relay
}

func (m *manager) find(id string) (*entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.relays {
		if e.relay.ID == id && !e.removed {
			return e, nil
		}
	}
	return nil, ErrNotFound
}

func (m *manager) entries() []*entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.relays)
}

func (m *manager) persist(ctx context.Context) error {
	m.mu.Lock()
	var out []Entry
	for _, e := range m.relays {
		r := e.snapshot()
		out = append(out, Entry{ID: r.ID, Label: r.Label, Addr: r.Addr})
	}
	m.mu.Unlock()
	return m.cfg.Store.Save(ctx, out)
}

// List implements Manager.
func (m *manager) List() []Relay {
	es := m.entries()
	out := make([]Relay, 0, len(es))
	for _, e := range es {
		out = append(out, e.snapshot())
	}
	return out
}

// Add implements Manager.
func (m *manager) Add(ctx context.Context, addr, label, claimCode string) (Relay, error) {
	ne, err := newEntry(Entry{Addr: addr, Label: label})
	if err != nil {
		return Relay{}, err
	}
	m.mu.Lock()
	var e *entry
	for _, old := range m.relays {
		if old.relay.ID == ne.relay.ID {
			e = old
		}
	}
	fresh := e == nil
	if fresh {
		e = ne
		m.relays = append(m.relays, e)
		if m.started {
			m.startDrain(e)
		}
	} else {
		e.mu.Lock()
		e.relay.Label, e.relay.Addr, e.info = label, addr, ne.info
		e.mu.Unlock()
	}
	m.mu.Unlock()
	if err := m.persist(ctx); err != nil {
		return Relay{}, err
	}
	return m.refresh(ctx, e, refreshOpts{claim: claimCode, repair: true})
}

// Remove implements Manager.
func (m *manager) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	idx := slices.IndexFunc(m.relays, func(e *entry) bool { return e.relay.ID == id })
	if idx < 0 {
		m.mu.Unlock()
		return ErrNotFound
	}
	e := m.relays[idx]
	m.relays = slices.Delete(m.relays, idx, idx+1)
	m.mu.Unlock()
	e.mu.Lock()
	e.removed, e.queue = true, nil
	e.mu.Unlock()
	return m.persist(ctx)
}

// Sync implements Manager.
func (m *manager) Sync(ctx context.Context, id string) (Relay, error) {
	e, err := m.find(id)
	if err != nil {
		return Relay{}, err
	}
	return m.refresh(ctx, e, refreshOpts{repair: true, force: true})
}

// Claim implements Manager.
func (m *manager) Claim(ctx context.Context, id, code string) (Relay, error) {
	e, err := m.find(id)
	if err != nil {
		return Relay{}, err
	}
	return m.refresh(ctx, e, refreshOpts{claim: code, repair: true})
}

// PeerAdded implements Manager.
func (m *manager) PeerAdded(p string) { m.enqueue(relayproto.OpAdd, p) }

// PeerRemoved implements Manager.
func (m *manager) PeerRemoved(p string) { m.enqueue(relayproto.OpRemove, p) }

func (m *manager) enqueue(op, p string) {
	id := m.lastOpID.Add(1)
	for _, e := range m.entries() {
		e.mu.Lock()
		e.queue = append(e.queue, setOp{id: id, op: op, peer: p})
		e.mu.Unlock()
		select {
		case e.kick <- struct{}{}:
		default:
		}
	}
}

// Start implements Manager.
func (m *manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return errors.New("p2prelaymgr: already started")
	}
	m.started = true
	m.ctx, m.cancel = context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, m.cancel)
	m.wg.Go(func() {
		<-m.ctx.Done()
		stop()
	})
	for _, e := range m.relays {
		m.startDrain(e)
	}
	m.wg.Go(m.loop)
	return nil
}

// Close implements Manager. It stops the loop and waits for it; it does not
// close the host.
func (m *manager) Close() error {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	return nil
}

func (m *manager) startDrain(e *entry) {
	m.wg.Go(func() {
		t := time.NewTicker(m.cfg.Retry)
		defer t.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-e.kick:
			case <-t.C:
			}
			m.flush(m.ctx, e)
		}
	})
}

// loop looks for lost connections and compares digests.
func (m *manager) loop() {
	m.refreshAll()
	t := time.NewTicker(m.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.refreshAll()
		}
	}
}

func (m *manager) refreshAll() {
	var wg sync.WaitGroup
	for _, e := range m.entries() {
		e.mu.Lock()
		due := time.Since(e.lastCheck) >= m.cfg.SyncInterval
		e.mu.Unlock()
		connected := m.lh().Network().Connectedness(e.info.ID) == network.Connected
		if connected && !due {
			continue
		}
		wg.Go(func() {
			// A new connection gets a full push; a due digest check repairs
			// only when the digests differ.
			_, _ = m.refresh(m.ctx, e, refreshOpts{repair: true, force: !connected})
		})
	}
	wg.Wait()
}

func (m *manager) nextSeq() int64 {
	for {
		last := m.lastSeq.Load()
		next := max(last+1, time.Now().UnixNano())
		if m.lastSeq.CompareAndSwap(last, next) {
			return next
		}
	}
}

// paired returns the paired peer IDs that parse.
func (m *manager) paired() []peer.ID {
	var ids []peer.ID
	for _, s := range m.cfg.PairedPeers() {
		id, err := peer.Decode(s)
		if err != nil {
			m.log.Warn("ignoring a paired peer with a bad ID", "peer", s)
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func (m *manager) pairedPeers(ids []peer.ID) []relayproto.Peer {
	out := make([]relayproto.Peer, 0, len(ids))
	for _, id := range ids {
		out = append(out, relayproto.Peer{ID: id.String()})
	}
	return out
}

// connect makes sure the host has a connection to the relay.
func (m *manager) connect(ctx context.Context, e *entry) error {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	return m.lh().Connect(ctx, e.info)
}

// call connects and sends one admin request.
func (m *manager) call(ctx context.Context, e *entry, req relayproto.Request) (relayproto.Response, error) {
	if err := m.connect(ctx, e); err != nil {
		return relayproto.Response{}, fmt.Errorf("connect to the relay: %w", err)
	}
	return relayproto.Call(ctx, m.lh(), e.info.ID, req)
}

// flush sends the queued adds and removes of a relay, in order, and stops at
// the first failure; the retry timer calls it again.
func (m *manager) flush(ctx context.Context, e *entry) {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	for ctx.Err() == nil {
		e.mu.Lock()
		if len(e.queue) == 0 || e.removed {
			e.mu.Unlock()
			return
		}
		head := e.queue[0]
		switch e.relay.State {
		case StateOpen, StateNotAdmin, StateDenied, StateUnclaimed:
			// Nothing to push: this server is not an admin of the relay. A
			// later claim or fix makes the next full push carry the change.
			e.queue = nil
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()

		resp, err := m.call(ctx, e, relayproto.Request{
			Op: head.op, Seq: m.nextSeq(), Peers: []relayproto.Peer{{ID: head.peer}},
		})
		if err != nil {
			m.log.Debug("relay change not sent yet", "relay", e.relay.ID, "op", head.op, "error", err)
			if resp.V == 0 {
				m.markUnreachable(e, err)
			}
			return
		}
		e.mu.Lock()
		if len(e.queue) > 0 && e.queue[0].id == head.id {
			e.queue = e.queue[1:]
		}
		e.mu.Unlock()
	}
}

func (m *manager) markUnreachable(e *entry, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.relay.State = StateUnreachable
	e.relay.Health = "cannot reach the relay: " + shorten(err)
	e.relay.CheckedAt = time.Now()
}

func shorten(err error) string {
	s := err.Error()
	if len(s) > 600 {
		s = s[:600] + "..."
	}
	return s
}

type refreshOpts struct {
	claim string
	// repair pushes the paired set when the relay's digest differs.
	repair bool
	// force pushes the paired set even when the digests match.
	force bool
	// readOnly changes nothing on the relay: no claim, no push.
	readOnly bool
}

// refresh reads the state of a relay and, for an admin, brings its set in
// line with the paired set. The error is non-nil for a refused claim code or
// a cancelled context; an unreachable relay is a state, not an error.
func (m *manager) refresh(ctx context.Context, e *entry, o refreshOpts) (Relay, error) {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	err := m.refreshLocked(ctx, e, o)
	return e.snapshot(), err
}

func (m *manager) set(e *entry, f func(r *Relay)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f(&e.relay)
	e.relay.CheckedAt = time.Now()
	e.lastCheck = time.Now()
}

func (m *manager) refreshLocked(ctx context.Context, e *entry, o refreshOpts) error {
	start := time.Now()
	e.mu.Lock()
	e.relay.State, e.relay.Health = StateChecking, "checking"
	e.mu.Unlock()

	if err := m.connect(ctx, e); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.set(e, func(r *Relay) {
			r.State, r.Health = StateUnreachable, "cannot connect: "+shorten(err)
		})
		return nil
	}
	resp, err := m.status(ctx, e)
	if err != nil {
		return m.statusFailed(ctx, e, resp, err)
	}
	latency := time.Since(start)

	if resp.Status != nil && resp.Status.Mode == relayproto.ModeUnclaimed && o.claim != "" && !o.readOnly {
		if _, err := relayproto.Call(ctx, m.lh(), e.info.ID, relayproto.Request{Op: relayproto.OpClaim, Code: o.claim}); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.set(e, func(r *Relay) {
				r.Mode, r.State, r.Health = ModeUnclaimed, StateUnclaimed, "the relay refused the claim code"
			})
			return ErrClaimRefused
		}
		if resp, err = m.status(ctx, e); err != nil {
			return m.statusFailed(ctx, e, resp, err)
		}
	}

	st := resp.Status
	if st == nil {
		m.set(e, func(r *Relay) { r.State, r.Health = StateUnreachable, "the relay sent no status" })
		return nil
	}
	apply := func(r *Relay) {
		r.Mode = st.Mode
		r.Latency = latency
		r.Admins, r.Members = st.Admins, st.Members
		r.Limits = limitsOf(st)
	}
	switch {
	case st.Mode == relayproto.ModeOpen:
		m.set(e, func(r *Relay) {
			apply(r)
			r.State, r.Health = StateOpen, "open relay: anyone can use it"
		})
	case st.Mode == relayproto.ModeUnclaimed:
		m.set(e, func(r *Relay) {
			apply(r)
			r.State, r.Health = StateUnclaimed, "the relay has no admin yet; claim it with the code it printed"
		})
	case !resp.Admin:
		m.set(e, func(r *Relay) {
			apply(r)
			r.State, r.Health = StateNotAdmin, "this server is a member of the relay, not an admin"
		})
	default:
		return m.syncAdmin(ctx, e, resp, apply, o)
	}
	return nil
}

func (m *manager) status(ctx context.Context, e *entry) (relayproto.Response, error) {
	return relayproto.Call(ctx, m.lh(), e.info.ID, relayproto.Request{Op: relayproto.OpStatus})
}

// statusFailed classifies a failed status call. A relay that answered with
// an error is reachable; a connection that opens and then closes is the
// relay's gate refusing this server; anything else is unreachable.
func (m *manager) statusFailed(ctx context.Context, e *entry, resp relayproto.Response, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case resp.V != 0:
		m.set(e, func(r *Relay) { r.Health = "the relay answered: " + resp.Err })
	case errors.Is(err, context.DeadlineExceeded):
		m.set(e, func(r *Relay) { r.State, r.Health = StateUnreachable, "the relay did not answer in time" })
	default:
		m.set(e, func(r *Relay) {
			r.State, r.Health = StateDenied, "the relay does not list this server as an admin or member"
		})
	}
	return nil
}

// syncAdmin compares the relay's digest with the paired set and repairs it.
func (m *manager) syncAdmin(ctx context.Context, e *entry, resp relayproto.Response, apply func(*Relay), o refreshOpts) error {
	upto := m.lastOpID.Load()
	ids := m.paired()
	want := relayproto.Digest(ids)
	inSync := resp.Digest == want
	if (!inSync && o.repair && !o.readOnly) || (o.force && !o.readOnly) {
		r, err := relayproto.Call(ctx, m.lh(), e.info.ID, relayproto.Request{
			Op: relayproto.OpReplace, Seq: m.nextSeq(), Peers: m.pairedPeers(ids),
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.set(e, func(rl *Relay) {
				apply(rl)
				rl.State, rl.Health = StateOutOfSync, "could not push the paired set: "+shorten(err)
			})
			return nil
		}
		inSync = r.Digest == want
		// The full push carries every change queued before it was built.
		e.mu.Lock()
		e.queue = slices.DeleteFunc(e.queue, func(op setOp) bool { return op.id <= upto })
		e.mu.Unlock()
	}
	m.set(e, func(r *Relay) {
		apply(r)
		if inSync {
			r.State, r.Health = StateInSync, "in sync"
		} else {
			r.State, r.Health = StateOutOfSync, "the relay's list differs from the paired devices"
		}
	})
	return nil
}
