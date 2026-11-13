package vz

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
)

// fakeMachine is a machine without a hypervisor. Its guest agent answers the
// requests of the host with ok, runs no process for real (an exec prints
// "hi" and exits 0), and boots in a few milliseconds.
type fakeMachine struct {
	cfg  vmm.Config
	fail error // Start returns it

	mu        sync.Mutex
	listeners map[uint32]func(net.Conn)
	started   bool
	calls     []string
	done      chan struct{}
	once      sync.Once
	// onStart runs when the machine starts, before the ready frame. A test
	// uses it to write to the console like a guest does.
	onStart func(cfg vmm.Config)
	// silent keeps the guest from sending the ready frame.
	silent bool
}

func newFakeMachine(cfg vmm.Config) *fakeMachine {
	return &fakeMachine{cfg: cfg, listeners: map[uint32]func(net.Conn){}, done: make(chan struct{})}
}

func (m *fakeMachine) Listen(port uint32, accept func(net.Conn)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listeners[port] = accept
	return nil
}

func (m *fakeMachine) Start(ctx context.Context) error {
	if m.fail != nil {
		return m.fail
	}
	m.mu.Lock()
	m.started = true
	ready := m.listeners[guestagent.AgentPort]
	m.mu.Unlock()
	if m.onStart != nil {
		m.onStart(m.cfg)
	}
	if ready != nil && !m.silent {
		go func() {
			host, guest := net.Pipe()
			ready(host)
			_ = guestagent.SendReady(guest)
			_ = guest.Close()
		}()
	}
	return nil
}

func (m *fakeMachine) Dial(ctx context.Context, port uint32) (net.Conn, error) {
	if port != guestagent.AgentPort {
		return nil, errors.New("fake: nothing listens on that port")
	}
	select {
	case <-m.done:
		return nil, errors.New("fake: the machine stopped")
	default:
	}
	host, guest := net.Pipe()
	go m.agent(guest)
	return host, nil
}

// agent answers one request of the host.
func (m *fakeMachine) agent(c net.Conn) {
	defer func() { _ = c.Close() }()
	f, err := guestagent.ReadFrame(c)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.calls = append(m.calls, f.Type)
	m.mu.Unlock()
	_ = guestagent.WriteFrame(c, guestagent.Frame{Type: guestagent.TypeOK})
	switch f.Type {
	case guestagent.TypeShutdown:
		m.Stop()
	case guestagent.TypeExec:
		if len(f.Args) > 0 && f.Args[0] == "sleep" {
			// A process that never exits by itself: it ends when the host
			// closes the connection or the machine stops.
			go func() {
				<-m.done
				_ = c.Close()
			}()
			_, _ = guestagent.ReadFrame(c)
			return
		}
		_ = guestagent.WriteFrame(c, guestagent.Frame{Type: guestagent.TypeStdout, Data: []byte("hi\n")})
		_ = guestagent.WriteFrame(c, guestagent.Frame{Type: guestagent.TypeExit})
		// Keep the connection until the host closes it, like a real guest.
		_, _ = guestagent.ReadFrame(c)
	}
}

func (m *fakeMachine) Done() <-chan struct{} { return m.done }

func (m *fakeMachine) Stop() { m.once.Do(func() { close(m.done) }) }

// seen counts the requests of a type that the guest agent answered.
func (m *fakeMachine) seen(typ string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c == typ {
			n++
		}
	}
	return n
}

// farm makes the machines of one test and keeps them.
type farm struct {
	mu       sync.Mutex
	machines []*fakeMachine
	// configure runs on each new machine before it is returned.
	configure func(m *fakeMachine)
}

func (f *farm) make(c vmm.Config) (machine, error) {
	m := newFakeMachine(c)
	if f.configure != nil {
		f.configure(m)
	}
	f.mu.Lock()
	f.machines = append(f.machines, m)
	f.mu.Unlock()
	return m, nil
}

func (f *farm) last() *fakeMachine {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.machines) == 0 {
		return nil
	}
	return f.machines[len(f.machines)-1]
}

func (f *farm) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.machines)
}
