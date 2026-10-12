// SPDX-License-Identifier: MIT
// Package runtime supervises trusted addon executables, independently of model scheduling.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var ErrRuntime = errors.New("addon runtime unavailable or invalid protocol")

type Config struct {
	StateRoot                  string // private owner-local durable process ledger, required
	Limits                     contract.Limits
	ReadyTimeout, StableHealth time.Duration
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
	accepted := make(chan bool, 1)
	go func() {
		i, e := s.start(wait, pkg, generation)
		done <- result{i, e}
		if keep := <-accepted; !keep && e == nil {
			_ = s.Stop(context.Background(), i)
		}
	}()
	select {
	case r := <-done:
		accepted <- true
		return r.i, r.e
	case <-wait.Done():
		accepted <- false
		return Instance{}, wait.Err()
	}
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
	host := contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH}
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
	defer listener.Close()
	socket, e := listener.File()
	if e != nil {
		return nil, ErrRuntime
	}
	defer socket.Close()
	child, parent, e := os.Pipe()
	if e != nil {
		return nil, ErrRuntime
	}
	defer child.Close()
	credential := make([]byte, 32)
	if _, e = rand.Read(credential); e != nil {
		parent.Close()
		return nil, ErrRuntime
	}
	inc := make([]byte, 16)
	if _, e = rand.Read(inc); e != nil {
		parent.Close()
		return nil, ErrRuntime
	}
	r := Registration{AddonID: pkg.Manifest.ID, Generation: generation, Incarnation: hex.EncodeToString(inc), Protocol: 1, Endpoint: "http://" + listener.Addr().String(), Capabilities: pkg.Manifest.Capabilities}
	// No shell, no inherited environment, no ambient host credentials. The argv
	// contract is fixed; package manifests cannot smuggle interpreter switches.
	cmd := exec.Command(path, "--localai-addon-v1")
	cmd.Dir = pkg.Directory
	cmd.Env = []string{"LANG=C", "TZ=UTC"}
	cmd.ExtraFiles = []*os.File{socket, child}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = s.ledger.put(pkg.Manifest.ID, processIdentity{Incarnation: r.Incarnation, Pending: true}); e != nil {
		parent.Close()
		return nil, ErrRuntime
	}
	if e = cmd.Start(); e != nil {
		_ = s.ledger.remove(pkg.Manifest.ID, r.Incarnation)
		parent.Close()
		return nil, ErrRuntime
	}
	p := &process{cmd: cmd, parent: parent, done: make(chan struct{}), registration: r}
	c, e := newClient(r, hex.EncodeToString(credential), s.config.Limits)
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		parent.Close()
		return nil, e
	}
	p.client = c
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
	e = json.NewEncoder(parent).Encode(Startup{Registration: r, Credential: c.credential, Limits: s.config.Limits})
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
func (s *Supervisor) monitor(ctx context.Context, slot *slot, p *process) {
	defer close(slot.workerDone)
	defer s.workers.Done()
	for {
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
					s.terminate(p)
					break healthLoop
				}
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
			next, e := s.launch(ctx, slot.pkg, slot.generation)
			if e != nil {
				continue
			}
			s.mu.Lock()
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
	s.mu.Unlock()
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
		slots = append(slots, slot)
	}
	s.mu.Unlock()
	for _, slot := range slots {
		s.mu.Lock()
		if e := s.stopLocked(ctx, slot); e != nil {
			return e
		}
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
