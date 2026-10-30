// SPDX-License-Identifier: MIT

// Package egress is the network proxy of the sandboxes. The guest has no
// network device; its tools are set to use a proxy on guest port 3128, and the
// guest agent forwards each connection to that port to a handler here. The
// handler reads the CONNECT or the absolute-form HTTP request, asks the host
// whether the chat may reach that host (host operation egress.check), dials
// through the shared guard of pkg/web/safehttp, and pipes the bytes. The host
// decides; the proxy only carries out the verdict and reports what moved
// (egress.audit). It never decrypts TLS.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

// Port is the guest port of the proxy. The guest image points its tools at
// http://127.0.0.1:3128.
const Port = 3128

// Limits of the proxy. A test may lower them.
type Limits struct {
	TunnelsPerSandbox int           // concurrent connections of one sandbox
	TunnelsTotal      int           // concurrent connections of the process
	Rate              float64       // new connections per second, per sandbox
	Burst             int           // burst of new connections, per sandbox
	Dial              time.Duration // one dial
	Idle              time.Duration // a tunnel with no bytes either way
	Header            time.Duration // reading a request head
	Hello             time.Duration // reading the TLS ClientHello
	HeaderBytes       int           // size of a request head
	UpBytes           int64         // bytes from the guest, per call
	DownBytes         int64         // bytes to the guest, per call
	CacheTTL          time.Duration // an allow verdict, within one call
	CheckTimeout      time.Duration // one egress.check, card wait included
}

// DefaultLimits are the limits of the design: 32 tunnels per sandbox, 256 per
// process, 20 new connections a second (burst 40), a 10 s dial, 120 s idle,
// 64 MiB up and 4 GiB down per call, an allow cached 30 s.
func DefaultLimits() Limits {
	return Limits{
		TunnelsPerSandbox: 32, TunnelsTotal: 256, Rate: 20, Burst: 40,
		Dial: 10 * time.Second, Idle: 120 * time.Second, Header: 30 * time.Second, Hello: 10 * time.Second,
		HeaderBytes: 16 << 10, UpBytes: 64 << 20, DownBytes: 4 << 30,
		CacheTTL: 30 * time.Second, CheckTimeout: 75 * time.Second,
	}
}

// CheckArgs are the arguments of egress.check. The chat and the user are not
// among them: the host takes them from the live request.
type CheckArgs struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Kind    string `json:"kind"`
	TLS     bool   `json:"tls"`
	Private bool   `json:"private,omitempty"`
}

// Verdict is the answer of egress.check.
type Verdict struct {
	Action       string `json:"action"` // allow, block
	Reason       string `json:"reason"`
	AllowPrivate bool   `json:"allowPrivate,omitempty"`
	Card         string `json:"card,omitempty"`
}

// Event is one line of egress.audit.
type Event struct {
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Kind       string    `json:"kind"`
	Verdict    string    `json:"verdict"` // done (an allowed connection ended) or block
	Reason     string    `json:"reason"`
	Addr       string    `json:"addr,omitempty"`
	BytesUp    int64     `json:"bytesUp,omitempty"`
	BytesDown  int64     `json:"bytesDown,omitempty"`
	DurationMS int64     `json:"durationMs,omitempty"`
	At         time.Time `json:"at"`
}

// MaxAuditEvents bounds one egress.audit call.
const MaxAuditEvents = 500

// Host is the host side the proxy asks. The add-on's main implements it over
// the host operation channel.
type Host interface {
	Check(ctx context.Context, operation string, a CheckArgs) (Verdict, error)
	Audit(ctx context.Context, operation string, evs []Event) error
}

// Options configures a Proxy.
type Options struct {
	Host Host
	// Resolver resolves host names for the guard. Nil: the system resolver.
	Resolver safehttp.Resolver
	// DenyAddrs lists the addresses of this machine. Nil: safehttp.SelfAddrs.
	DenyAddrs func() []netip.Prefix
	// TreatAsPublic are addresses the guard lets through as public. Tests
	// only.
	TreatAsPublic []netip.Addr
	Limits        Limits
	Now           func() time.Time
	// TLSPort is the port whose tunnels must start with a TLS ClientHello
	// that names the CONNECT host. Zero: 443. Tests set the port of their
	// TLS server.
	TLSPort int
}

// Proxy serves port 3128 of every sandbox of the add-on process.
type Proxy struct {
	o     Options
	deny  func() []netip.Prefix
	mu    sync.Mutex
	boxes map[string]*box
	open  int // tunnels of the process
}

// New returns a proxy. Zero limits take the defaults.
func New(o Options) *Proxy {
	d := DefaultLimits()
	l := &o.Limits
	if l.TunnelsPerSandbox <= 0 {
		l.TunnelsPerSandbox = d.TunnelsPerSandbox
	}
	if l.TunnelsTotal <= 0 {
		l.TunnelsTotal = d.TunnelsTotal
	}
	if l.Rate <= 0 {
		l.Rate = d.Rate
	}
	if l.Burst <= 0 {
		l.Burst = d.Burst
	}
	if l.Dial <= 0 {
		l.Dial = d.Dial
	}
	if l.Idle <= 0 {
		l.Idle = d.Idle
	}
	if l.Header <= 0 {
		l.Header = d.Header
	}
	if l.Hello <= 0 {
		l.Hello = d.Hello
	}
	if l.HeaderBytes <= 0 {
		l.HeaderBytes = d.HeaderBytes
	}
	if l.UpBytes <= 0 {
		l.UpBytes = d.UpBytes
	}
	if l.DownBytes <= 0 {
		l.DownBytes = d.DownBytes
	}
	if l.CacheTTL <= 0 {
		l.CacheTTL = d.CacheTTL
	}
	if l.CheckTimeout <= 0 {
		l.CheckTimeout = d.CheckTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.TLSPort == 0 {
		o.TLSPort = 443
	}
	p := &Proxy{o: o, boxes: map[string]*box{}}
	p.deny = o.DenyAddrs
	if p.deny == nil {
		p.deny = safehttp.SelfAddrs(nil)
	}
	return p
}

// Key names the sandbox of a user and a conversation.
func Key(user, conv string) string { return user + "\x00" + conv }

// box is the state of one sandbox: the call in progress and what it used.
type box struct {
	mu      sync.Mutex
	op      string
	callCtx context.Context
	cancel  context.CancelFunc // ends the tunnels of the call
	conns   sync.WaitGroup     // handlers of the call
	tunnels int
	tokens  float64
	last    time.Time
	up      int64
	down    int64
	cache   map[string]time.Time // allowed host:port of the call, until
	events  []Event              // audit lines of the call, not sent yet
	idle    []Event              // connections while no call ran
}

func (p *Proxy) box(key string) *box {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.boxes[key]
	if b == nil {
		b = &box{cache: map[string]time.Time{}, tokens: float64(p.o.Limits.Burst)}
		p.boxes[key] = b
	}
	return b
}

// Ports returns the port handlers of a sandbox: the proxy on Port. The set is
// the same whatever the chat's preset, since Resume needs a handler for each
// port the sandbox was made with; the preset is enforced by the verdict.
func (p *Proxy) Ports(user, conv string) map[int]sandbox.PortHandler {
	key := Key(user, conv)
	return map[int]sandbox.PortHandler{Port: func(ctx context.Context, conn net.Conn) { p.serve(ctx, key, conn) }}
}

// Begin marks the call in progress of a sandbox. It sends the connections
// counted while no call ran (an idle sandbox has no live operation to send
// them with). The returned end closes every tunnel of the call, sends the
// audit of the call and clears the mark; it may be called more than once.
func (p *Proxy) Begin(ctx context.Context, user, conv, op string) (end func()) {
	b := p.box(Key(user, conv))
	callCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	b.mu.Lock()
	b.op, b.callCtx, b.cancel = op, callCtx, cancel
	b.up, b.down = 0, 0
	b.cache = map[string]time.Time{}
	idle := b.idle
	b.idle = nil
	b.mu.Unlock()
	if len(idle) > 0 {
		p.send(ctx, op, idle)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			done := make(chan struct{})
			go func() { b.conns.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
			b.mu.Lock()
			evs := b.events
			b.events = nil
			b.op, b.callCtx, b.cancel = "", nil, nil
			b.cache = map[string]time.Time{}
			b.mu.Unlock()
			if len(evs) > 0 {
				p.send(ctx, op, evs)
			}
		})
	}
}

// send reports audit lines, in calls of at most MaxAuditEvents. A failure is
// dropped: the host audited each verdict when it gave it.
func (p *Proxy) send(ctx context.Context, op string, evs []Event) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for len(evs) > 0 {
		n := min(len(evs), MaxAuditEvents)
		_ = p.o.Host.Audit(ctx, op, evs[:n])
		evs = evs[n:]
	}
}

// record keeps an audit line of the call, or of the idle time.
func (b *box) record(ev Event, idle bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if idle {
		if len(b.idle) < MaxAuditEvents {
			b.idle = append(b.idle, ev)
		}
		return
	}
	if len(b.events) < 4*MaxAuditEvents {
		b.events = append(b.events, ev)
	}
}

// admit takes a token of the rate limit and a tunnel place, or says no.
func (p *Proxy) admit(b *box) bool {
	now := p.o.Now()
	l := p.o.Limits
	b.mu.Lock()
	if !b.last.IsZero() {
		b.tokens = min(float64(l.Burst), b.tokens+now.Sub(b.last).Seconds()*l.Rate)
	}
	b.last = now
	if b.tokens < 1 || b.tunnels >= l.TunnelsPerSandbox {
		b.mu.Unlock()
		return false
	}
	p.mu.Lock()
	if p.open >= l.TunnelsTotal {
		p.mu.Unlock()
		b.mu.Unlock()
		return false
	}
	p.open++
	p.mu.Unlock()
	b.tokens--
	b.tunnels++
	b.mu.Unlock()
	return true
}

func (p *Proxy) release(b *box) {
	p.mu.Lock()
	p.open--
	p.mu.Unlock()
	b.mu.Lock()
	b.tunnels--
	b.mu.Unlock()
}

// call returns the operation and the context of the call in progress, and
// joins its wait group; done leaves it.
func (b *box) call() (op string, ctx context.Context, done func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.op == "" {
		return "", nil, func() {}
	}
	b.conns.Add(1)
	return b.op, b.callCtx, b.conns.Done
}

// cached says whether an allow of host:port is cached for the call.
func (p *Proxy) cached(b *box, k string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.cache[k]
	return ok && p.o.Now().Before(until)
}

func (p *Proxy) cache(b *box, k string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cache[k] = p.o.Now().Add(p.o.Limits.CacheTTL)
}

// verdict asks the host, or uses an allow cached for the call. A block or an
// ask is never cached, so a card answer applies at once. A host that does not
// answer blocks.
func (p *Proxy) verdict(ctx context.Context, b *box, op string, a CheckArgs) Verdict {
	k := net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
	if !a.Private && p.cached(b, k) {
		return Verdict{Action: "allow", Reason: "cached"}
	}
	cctx, cancel := context.WithTimeout(ctx, p.o.Limits.CheckTimeout)
	defer cancel()
	v, err := p.o.Host.Check(cctx, op, a)
	if err != nil {
		return Verdict{Action: "block", Reason: "unavailable"}
	}
	if v.Action == "allow" && !a.Private && !v.AllowPrivate {
		p.cache(b, k)
	}
	return v
}

// guard returns the guard of one port and verdict.
func (p *Proxy) guard(port int, allowPrivate bool) *safehttp.Guard {
	return safehttp.NewGuard(safehttp.GuardPolicy{AllowPrivate: allowPrivate, Ports: []int{port}, DenyAddrs: p.deny, TreatAsPublic: p.o.TreatAsPublic}, p.o.Resolver)
}

// dialReason maps a dial error of the guard to an audit reason.
func dialReason(err error) string {
	switch {
	case errors.Is(err, safehttp.ErrMetadataAddress):
		return "metadata"
	case errors.Is(err, safehttp.ErrSelfAddress):
		return "self"
	case errors.Is(err, safehttp.ErrBlockedAddress):
		return "private"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) || errors.Is(err, safehttp.ErrURL) {
		return "resolve"
	}
	return "dial"
}

// dial connects to host:port for a verdict. When the guard finds private
// answers and the verdict did not allow them, the host is asked again with
// Private set: only an administrator's rule or answer, with the private
// switch on, lets it through.
func (p *Proxy) dial(ctx context.Context, b *box, op string, a CheckArgs, v Verdict) (net.Conn, Verdict, error) {
	dctx, cancel := context.WithTimeout(ctx, p.o.Limits.Dial)
	conn, err := p.guard(a.Port, v.AllowPrivate).DialContext(dctx, "tcp", net.JoinHostPort(a.Host, strconv.Itoa(a.Port)))
	cancel()
	if err == nil || v.AllowPrivate || dialReason(err) != "private" {
		return conn, v, err
	}
	a.Private = true
	v2 := p.verdict(ctx, b, op, a)
	if v2.Action != "allow" || !v2.AllowPrivate {
		return nil, v2, fmt.Errorf("%w (private)", err)
	}
	dctx, cancel = context.WithTimeout(ctx, p.o.Limits.Dial)
	defer cancel()
	conn, err = p.guard(a.Port, true).DialContext(dctx, "tcp", net.JoinHostPort(a.Host, strconv.Itoa(a.Port)))
	return conn, v2, err
}
