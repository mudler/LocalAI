// SPDX-License-Identifier: MIT
// Package runtime supervises trusted addon executables, independently of model scheduling.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var ErrRuntime = errors.New("addon runtime unavailable or invalid protocol")

// ErrConflict is the add-on's own refusal (HTTP 409) of a request that
// conflicts with a record it holds, such as an operation ID already used
// for another request. Nothing was changed. It is an ErrRuntime for every
// caller that does not tell refusals apart.
var ErrConflict = fmt.Errorf("addon refused the request as a conflict: %w", ErrRuntime)

// ErrNotSent says the request never reached the wire: no admission slot, a
// withdrawn client, an envelope that failed validation, or a connection that
// was never written to. The add-on cannot have seen the request, so a caller
// may try again and must not record an unknown outcome. It is an ErrRuntime
// for every caller that does not tell the two apart. A failure after the
// first request byte may have been written is never an ErrNotSent.
var ErrNotSent = fmt.Errorf("addon request was not sent: %w", ErrRuntime)

// ErrRefused is the add-on's own refusal (HTTP 400 or 422) of a request it
// judged invalid. It is deterministic: the add-on changed nothing and the same
// request is refused again, so it is neither an outage nor an unknown outcome.
// It is not an ErrRuntime.
var ErrRefused = fmt.Errorf("addon refused the request: %w", contract.ErrRefused)

// Refusal is the typed form of ErrRefused. Reason is a short printable class
// taken from the add-on's answer and bounded, so it is safe to log.
type Refusal struct {
	Status int
	Reason string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("addon refused the request (%d): %s", r.Status, r.Reason)
}
func (r *Refusal) Unwrap() error { return ErrRefused }

type Config struct {
	StateRoot string // private owner-local durable process ledger, required
	// DataRoot holds one private directory per addon ID. When set, the
	// supervisor creates the directory (mode 0700) and tells the addon its
	// path in Startup.DataDir. Empty means the addon is given no data directory.
	DataRoot                   string
	Limits                     contract.Limits
	ReadyTimeout, StableHealth time.Duration
	// HostOps, when set, gives each add-on that its Allowed map names a
	// host operation endpoint. Nil means no add-on gets one.
	HostOps *HostOps
}

// HostOps wires the host operation channel into the supervisor. Allowed maps
// an add-on ID to the operation prefixes it may call; an add-on that is not a
// key gets no endpoint and no credential. InFlight records the envelope of
// each request while the add-on serves it.
type HostOps struct {
	Broker   *hostops.Broker
	InFlight *hostops.InFlight
	Allowed  map[string][]string
}

func (h *HostOps) prefixes(id string) []string {
	if h == nil || h.Broker == nil || h.InFlight == nil {
		return nil
	}
	return h.Allowed[id]
}

type Registration struct {
	AddonID      string                `json:"addonId"`
	Generation   uint64                `json:"generation"`
	Incarnation  string                `json:"incarnation"`
	Protocol     int                   `json:"protocol"`
	Endpoint     string                `json:"endpoint"`
	Capabilities contract.Capabilities `json:"capabilities"`
}

// Startup is delivered only on inherited descriptor 4, never argv or environment.
// Descriptor 3 is an already-bound loopback listener. EOF on descriptor 4 means
// the owner has died; cooperating addons must exit rather than become orphans.
type Startup struct {
	Registration Registration    `json:"registration"`
	Credential   string          `json:"credential"`
	Limits       contract.Limits `json:"limits"`
	// DataDir is the addon's private mutable data directory, an absolute path
	// the owner created. The package directory is immutable and never holds data.
	DataDir string `json:"dataDir,omitempty"`
	// HostEndpoint and HostCredential give an add-on that the host setting
	// names a loopback endpoint for host operations, with a credential of its
	// own. Both are absent for every other add-on.
	HostEndpoint   string `json:"hostEndpoint,omitempty"`
	HostCredential string `json:"hostCredential,omitempty"`
}
type Instance struct {
	PID         int
	Incarnation string
	Generation  uint64
	Client      contract.Runtime
	process     *process
	slot        *slot
}
type process struct {
	cmd          *exec.Cmd
	parent       *os.File
	done         chan struct{}
	client       *Client
	registration Registration
	readyAt      time.Time
}
type slot struct {
	owner      *Supervisor
	pkg        packages.PackageReceipt
	generation uint64
	current    *process
	stopped    bool
	cancel     context.CancelFunc
	attempts   int
	original   *process
	// launching is true while a launch of this slot is between exec and
	// readiness, for the first start and for every restart.
	launching  bool
	workerDone chan struct{}
	stopDone   chan struct{}
	stopErr    error
}
type Registry struct{ supervisor *Supervisor }
type Supervisor struct {
	ledger        *processLedger
	closeMu       sync.Mutex
	ledgerClosed  bool
	mu            sync.Mutex
	config        Config
	slots         map[string]*slot
	closed        bool
	Registry      *Registry
	workers       sync.WaitGroup
	launchStarted func(*process) // deterministic launch-boundary instrumentation for subprocess tests
}

func NewSupervisor(c Config) (*Supervisor, error) {
	if c.Limits.Validate() != nil || c.ReadyTimeout <= 0 || c.StableHealth <= 0 {
		return nil, ErrRuntime
	}
	l, e := openLedger(c.StateRoot, c.Limits.ShutdownTimeout)
	if e != nil {
		return nil, e
	}
	s := &Supervisor{config: c, slots: map[string]*slot{}, ledger: l}
	s.Registry = &Registry{supervisor: s}
	return s, nil
}
func (r *Registry) Snapshot(ctx context.Context) ([]Registration, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s := r.supervisor
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Registration{}
	for _, slot := range s.slots {
		p := slot.current
		if slot.stopped || p == nil {
			continue
		}
		select {
		case <-p.done:
			continue
		default:
		}
		if !p.client.admitting() {
			continue
		}
		b, _ := json.Marshal(p.registration)
		var copy Registration
		_ = json.Unmarshal(b, &copy)
		result = append(result, copy)
	}
	return result, nil
}
func (s *Supervisor) Start(ctx context.Context, pkg packages.PackageReceipt, generation uint64) (Instance, error) {
	wait, cancel := context.WithTimeout(ctx, s.config.ReadyTimeout+2*s.config.Limits.ShutdownTimeout)
	defer cancel()
	type result struct {
		i Instance
		e error
	}
	done := make(chan result, 1)
	// A canceled caller still waits for the launch to finish cleaning up. The
	// slot of the add-on stays reserved until then, and a caller that must
	// start another generation right away would otherwise be refused. The wait
	// is bounded: the launch itself stops at the ready timeout.
	go func() {
		i, e := s.start(wait, pkg, generation)
		if e == nil && wait.Err() != nil {
			// The caller is gone, but the launched process must still be stopped.
			_ = s.Stop(context.WithoutCancel(ctx), i)
			i, e = Instance{}, wait.Err()
		}
		done <- result{i, e}
	}()
	r := <-done
	return r.i, r.e
}

func (s *Supervisor) start(ctx context.Context, pkg packages.PackageReceipt, generation uint64) (Instance, error) {
	if generation == 0 {
		return Instance{}, ErrRuntime
	}
	if e := ctx.Err(); e != nil {
		return Instance{}, e
	}
	s.mu.Lock()
	if s.closed || s.slots[pkg.Manifest.ID] != nil {
		s.mu.Unlock()
		return Instance{}, ErrRuntime
	}
	life, cancel := context.WithCancel(context.Background())
	slot := &slot{owner: s, pkg: pkg, generation: generation, cancel: cancel, workerDone: make(chan struct{}), stopDone: make(chan struct{})}
	slot.launching = true
	s.slots[pkg.Manifest.ID] = slot
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	startup, stop := context.WithCancel(ctx)
	end := context.AfterFunc(life, stop)
	defer end()
	defer stop()
	p, e := s.launch(startup, pkg, generation)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e != nil || ctx.Err() != nil || slot.stopped || s.closed {
		cancel()
		s.mu.Unlock()
		if p != nil {
			s.terminate(p)
		}
		s.mu.Lock()
		if s.slots[pkg.Manifest.ID] == slot {
			delete(s.slots, pkg.Manifest.ID)
		}
		close(slot.workerDone)
		return Instance{}, ErrRuntime
	}
	slot.launching = false
	slot.current = p
	slot.original = p
	i := Instance{PID: p.cmd.Process.Pid, Incarnation: p.registration.Incarnation, Generation: generation, Client: p.client, process: p, slot: slot}
	s.workers.Add(1)
	go s.monitor(life, slot, p)
	return i, nil
}
func (s *Supervisor) launch(ctx context.Context, pkg packages.PackageReceipt, generation uint64) (*process, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.ReadyTimeout)
	defer cancel()
	host := contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: contract.HostSDKVersion, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH}
	if contract.ValidateManifest(pkg.Manifest, host) != nil || !filepath.IsAbs(pkg.Directory) {
		return nil, ErrRuntime
	}
	// Receipts come from the owner journal. Reject substitutions before executing.
	b, e := os.ReadFile(filepath.Join(pkg.Directory, "receipt.json"))
	if e != nil || len(b) > 1<<20 {
		return nil, ErrRuntime
	}
	var disk packages.PackageReceipt
	if json.Unmarshal(b, &disk) != nil || !reflect.DeepEqual(disk, pkg) {
		return nil, ErrRuntime
	}
	path := filepath.Join(pkg.Directory, filepath.FromSlash(pkg.Manifest.Executable))
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return nil, ErrRuntime
	}
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, ErrRuntime
	}
	listener, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		return nil, ErrRuntime
	}
	defer func() { _ = listener.Close() }()
	socket, e := listener.File()
	if e != nil {
		return nil, ErrRuntime
	}
	defer func() { _ = socket.Close() }()
	// The host operation endpoint belongs to the incarnation: only an add-on
	// that the host setting names gets one. The listener is closed on every
	// failed launch, and by serveHostOps when the process ends.
	var hostListener net.Listener
	var hostCredential string
	hostPrefixes := s.config.HostOps.prefixes(pkg.Manifest.ID)
	hostReady := false
	if len(hostPrefixes) > 0 {
		hostListener, e = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			return nil, ErrRuntime
		}
		defer func() {
			if !hostReady {
				_ = hostListener.Close()
			}
		}()
		hc := make([]byte, 32)
		if _, e = rand.Read(hc); e != nil {
			return nil, ErrRuntime
		}
		hostCredential = hex.EncodeToString(hc)
	}
	child, parent, e := os.Pipe()
	if e != nil {
		return nil, ErrRuntime
	}
	defer func() { _ = child.Close() }()
	credential := make([]byte, 32)
	if _, e = rand.Read(credential); e != nil {
		_ = parent.Close()
		return nil, ErrRuntime
	}
	inc := make([]byte, 16)
	if _, e = rand.Read(inc); e != nil {
		_ = parent.Close()
		return nil, ErrRuntime
	}
	r := Registration{AddonID: pkg.Manifest.ID, Generation: generation, Incarnation: hex.EncodeToString(inc), Protocol: 1, Endpoint: "http://" + listener.Addr().String(), Capabilities: pkg.Manifest.Capabilities}
	dataDir := ""
	if s.config.DataRoot != "" {
		if !filepath.IsAbs(s.config.DataRoot) || !relativeSafe(pkg.Manifest.ID) {
			return nil, ErrRuntime
		}
		dataDir = filepath.Join(s.config.DataRoot, pkg.Manifest.ID)
		if os.MkdirAll(dataDir, 0o700) != nil {
			return nil, ErrRuntime
		}
		if fi, e := os.Lstat(dataDir); e != nil || !fi.IsDir() {
			return nil, ErrRuntime
		}
	}
	// No shell, no inherited environment, no ambient host credentials. The argv
	// contract is fixed; package manifests cannot smuggle interpreter switches.
	cmd := exec.Command(path, "--localai-addon-v1") // #nosec G204 -- launches the verified add-on executable from the installed package directory, never from a request
	cmd.Dir = pkg.Directory
	cmd.Env = []string{"LANG=C", "TZ=UTC"}
	cmd.ExtraFiles = []*os.File{socket, child}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = s.ledger.put(pkg.Manifest.ID, processIdentity{Incarnation: r.Incarnation, Pending: true}); e != nil {
		_ = parent.Close()
		return nil, ErrRuntime
	}
	if e = cmd.Start(); e != nil {
		_ = s.ledger.remove(pkg.Manifest.ID, r.Incarnation)
		_ = parent.Close()
		return nil, ErrRuntime
	}
	p := &process{cmd: cmd, parent: parent, done: make(chan struct{}), registration: r}
	c, e := newClient(r, hex.EncodeToString(credential), s.config.Limits)
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = parent.Close()
		return nil, e
	}
	p.client = c
	if hostListener != nil {
		if hostCredential == c.credential {
			s.terminate(p)
			return nil, ErrRuntime
		}
		c.inflight = s.config.HostOps.InFlight
		s.serveHostOps(p, hostListener, pkg.Manifest.ID, hostCredential, hostPrefixes)
	}
	ident, identityErr := identity(cmd.Process.Pid)
	ident.Incarnation = r.Incarnation
	if identityErr == nil {
		identityErr = s.ledger.put(pkg.Manifest.ID, ident)
	}
	go func() { _ = cmd.Wait(); c.withdraw(); close(p.done) }()
	if identityErr != nil {
		s.terminate(p)
		return nil, ErrRuntime
	}
	s.mu.Lock()
	hook := s.launchStarted
	s.mu.Unlock()
	if hook != nil {
		hook(p)
	}
	// The startup object is bounded; write with a deadline so even a child that
	// never reads its inherited pipe cannot hang the owner.
	_ = parent.SetWriteDeadline(time.Now().Add(s.config.ReadyTimeout))
	startupObject := Startup{Registration: r, Credential: c.credential, Limits: s.config.Limits, DataDir: dataDir}
	if hostListener != nil {
		startupObject.HostEndpoint, startupObject.HostCredential = "http://"+hostListener.Addr().String(), hostCredential
	}
	e = json.NewEncoder(parent).Encode(startupObject)
	if e != nil {
		s.terminate(p)
		return nil, ErrRuntime
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c.health(ctx, r) == nil {
			select {
			case <-p.done:
				s.terminate(p)
				return nil, ErrRuntime
			default:
			}
			c.mu.Lock()
			c.accept = true
			c.mu.Unlock()
			p.readyAt = time.Now()
			hostReady = true
			return p, nil
		}
		select {
		case <-ctx.Done():
			s.terminate(p)
			return nil, ErrRuntime
		case <-p.done:
			s.terminate(p)
			return nil, ErrRuntime
		case <-ticker.C:
		}
	}
}

// serveHostOps serves the host operation channel of one incarnation and
// closes it when the process ends. terminate waits for the process, so the
// endpoint never outlives it.
func (s *Supervisor) serveHostOps(p *process, l net.Listener, id, credential string, prefixes []string) {
	srv := &http.Server{Handler: s.config.HostOps.Broker.Handler(id, credential, prefixes), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = srv.Serve(l) }()
	go func() { <-p.done; _ = srv.Close() }()
}

func (s *Supervisor) terminate(p *process) {
	p.client.withdraw()
	_ = p.parent.Close()
	// os.Process owns the OS process handle (pidfd on supported Linux). Never
	// reconstruct a Process from a recorded numeric PID, which may be reused.
	_ = p.cmd.Process.Kill()
	<-p.done
	p.client.http.CloseIdleConnections()
	_ = s.ledger.remove(p.registration.AddonID, p.registration.Incarnation)
}

// healthMissesToKill is how many probes in a row must fail before the
// supervisor kills a running add-on.
const healthMissesToKill = 3

func (s *Supervisor) monitor(ctx context.Context, slot *slot, p *process) {
	defer close(slot.workerDone)
	defer s.workers.Done()
	for {
		misses := 0
		ticker := time.NewTicker(100 * time.Millisecond)
	healthLoop:
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-p.done:
				break healthLoop
			case <-ticker.C:
				if p.client.health(ctx, p.registration) != nil {
					if ctx.Err() != nil {
						ticker.Stop()
						return
					}
					// One missed probe is a busy add-on, not a dead one:
					// killing it would drop the work it is serving.
					misses++
					if misses < healthMissesToKill {
						continue
					}
					s.terminate(p)
					break healthLoop
				}
				misses = 0
				if time.Since(p.readyAt) >= s.config.StableHealth {
					s.mu.Lock()
					slot.attempts = 0
					s.mu.Unlock()
				}
			}
		}
		ticker.Stop()
		s.mu.Lock()
		if slot.stopped || s.closed {
			s.mu.Unlock()
			return
		}
		slot.current = nil
		s.mu.Unlock()
		s.terminate(p)
		for {
			s.mu.Lock()
			if slot.stopped || s.closed || int64(slot.attempts) >= s.config.Limits.BackoffAttempts {
				s.mu.Unlock()
				return
			}
			slot.attempts++
			n := slot.attempts
			s.mu.Unlock()
			delay := s.config.Limits.BackoffInitial
			for j := 1; j < n && delay < s.config.Limits.BackoffMax; j++ {
				if delay > s.config.Limits.BackoffMax/2 {
					delay = s.config.Limits.BackoffMax
				} else {
					delay *= 2
				}
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			s.mu.Lock()
			slot.launching = true
			s.mu.Unlock()
			next, e := s.launch(ctx, slot.pkg, slot.generation)
			// The launch flag is cleared in the same critical section that
			// publishes the process, so an observer never sees neither.
			s.mu.Lock()
			slot.launching = false
			if e != nil {
				s.mu.Unlock()
				continue
			}
			if slot.stopped || s.closed {
				s.mu.Unlock()
				s.terminate(next)
				return
			}
			slot.current = next
			s.mu.Unlock()
			p = next
			break
		}
	}
}

// Observed process states. They are an observation of the supervisor, not
// durable intent.
const (
	StateRunning  = "running"
	StateStarting = "starting"
	StateFailed   = "failed"
	StateStopped  = "stopped"
)

// State observes the process of one addon generation. It never starts,
// restarts or stops anything.
//
//   - running: the incarnation is alive and admitting calls.
//   - starting: a launch is between exec and readiness.
//   - failed: the process is not serving and no launch is under way. The
//     supervisor restarts it after its backoff while the budget lasts, so
//     failed can be followed by starting; once the budget is spent it stays.
//   - stopped: no process of this generation is held, or it is draining.
func (s *Supervisor) State(id string, generation uint64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[id]
	if s.closed || slot == nil || slot.generation != generation || slot.stopped {
		return StateStopped
	}
	if slot.launching {
		return StateStarting
	}
	p := slot.current
	if p == nil {
		return StateFailed
	}
	select {
	case <-p.done:
		return StateFailed
	default:
	}
	if !p.client.admitting() {
		return StateFailed
	}
	return StateRunning
}
func (s *Supervisor) Attempts(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.slots[id]; v != nil {
		return v.attempts
	}
	return 0
}
func (s *Supervisor) Stop(ctx context.Context, i Instance) error {
	s.mu.Lock()
	slot := i.slot
	if slot == nil || slot.owner != s || i.process == nil || (slot.original != i.process && slot.current != i.process) || i.Incarnation != i.process.registration.Incarnation || i.PID != i.process.cmd.Process.Pid || i.Generation != slot.generation {
		s.mu.Unlock()
		return ErrRuntime
	}
	return s.stopLocked(ctx, slot)
}

// The slot remains reserved until both the launch worker and its last child are
// finished. A caller timeout does not release the reservation or claim success.
func (s *Supervisor) stopLocked(ctx context.Context, slot *slot) error {
	s.beginStopLocked(slot)
	s.mu.Unlock()
	return s.waitStop(ctx, slot)
}

// Initiate cleanup without waiting so Close can withdraw every slot atomically.
func (s *Supervisor) beginStopLocked(slot *slot) {
	if !slot.stopped {
		slot.stopped = true
		slot.cancel()
		if slot.current != nil {
			slot.current.client.withdraw()
		}
		go func() {
			<-slot.workerDone
			s.mu.Lock()
			p := slot.current
			s.mu.Unlock()
			if p != nil {
				drain, cancel := context.WithTimeout(context.Background(), s.config.Limits.ShutdownTimeout)
				p.client.drain(drain)
				cancel()
				s.terminate(p)
			}
			s.mu.Lock()
			slot.current = nil
			slot.stopErr = s.ledger.remove(slot.pkg.Manifest.ID, func() string {
				if p != nil {
					return p.registration.Incarnation
				}
				return ""
			}())
			if slot.stopErr == nil && s.slots[slot.pkg.Manifest.ID] == slot {
				delete(s.slots, slot.pkg.Manifest.ID)
			}
			close(slot.stopDone)
			s.mu.Unlock()
		}()
	}
}

func (s *Supervisor) waitStop(ctx context.Context, slot *slot) error {
	// Draining and OS cleanup each receive one finite shutdown interval.
	wait, cancel := context.WithTimeout(ctx, 2*s.config.Limits.ShutdownTimeout)
	defer cancel()
	select {
	case <-slot.stopDone:
		return slot.stopErr
	case <-wait.Done():
		return wait.Err()
	}
}

// Current explicitly obtains a ready incarnation. Previously returned clients
// never follow restarts, so callers cannot accidentally replay a stale mutation.
func (s *Supervisor) Current(ctx context.Context, id string, generation uint64) (Instance, error) {
	if e := ctx.Err(); e != nil {
		return Instance{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[id]
	if s.closed || slot == nil || slot.stopped || slot.generation != generation || slot.current == nil {
		return Instance{}, ErrRuntime
	}
	p := slot.current
	select {
	case <-p.done:
		return Instance{}, ErrRuntime
	default:
	}
	if !p.client.admitting() {
		return Instance{}, ErrRuntime
	}
	return Instance{PID: p.cmd.Process.Pid, Incarnation: p.registration.Incarnation, Generation: generation, Client: p.client, process: p, slot: slot}, nil
}
func (s *Supervisor) Close(ctx context.Context) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	s.closed = true
	slots := make([]*slot, 0, len(s.slots))
	for _, slot := range s.slots {
		s.beginStopLocked(slot)
		slots = append(slots, slot)
	}
	s.mu.Unlock()
	var cleanupErr error
	for _, slot := range slots {
		cleanupErr = errors.Join(cleanupErr, s.waitStop(ctx, slot))
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	done := make(chan struct{})
	go func() { s.workers.Wait(); close(done) }()
	wait, cancel := context.WithTimeout(ctx, 2*s.config.Limits.ShutdownTimeout)
	defer cancel()
	select {
	case <-done:
	case <-wait.Done():
		return wait.Err()
	}
	if !s.ledgerClosed {
		s.ledgerClosed = true
		return s.ledger.close()
	}
	return nil
}

// relativeSafe reports an addon ID that is one plain path element.
func relativeSafe(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}
