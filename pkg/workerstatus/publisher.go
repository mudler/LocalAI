// SPDX-License-Identifier: MIT
package workerstatus

import (
	"context"
	"os"
	"sync"
	"time"
)

// Publisher serializes transitions and periodic snapshots. A persistence failure
// is terminal: remove old evidence and notify the worker to shut down. Readers
// also expire a snapshot whose sequence no longer advances.
type Publisher struct {
	mu     sync.Mutex
	path   string
	state  Snapshot
	failed bool
	cancel context.CancelFunc
	probe  func() (connected, authenticated, subscriptions, ready bool)
}

func FromEnvironment(ctx context.Context, cancel context.CancelFunc) (*Publisher, error) {
	path, generation := os.Getenv(PathEnv), os.Getenv(GenerationEnv)
	if path == "" && generation == "" {
		return nil, nil
	}
	return NewPublisher(ctx, path, generation, cancel)
}

// Generation returns the launch generation this publisher reports, or "" for a
// nil publisher (a worker not managed by a launcher).
func (p *Publisher) Generation() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state.Generation
}

func NewPublisher(ctx context.Context, path, generation string, cancel context.CancelFunc) (*Publisher, error) {
	p := &Publisher{path: path, cancel: cancel, state: Snapshot{PID: os.Getpid(), Schema: Schema, Generation: generation, Registration: "starting", HeartbeatInterval: 10 * time.Second}}
	// A fresh launch must never adopt a previous session's file.
	if err := requireNoSnapshot(path); err != nil {
		return nil, err
	}
	if err := p.publishLocked(); err != nil {
		return nil, err
	}
	go func() {
		ticker := time.NewTicker(PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.Update(nil)
			}
		}
	}()
	return p, nil
}
func (p *Publisher) publishLocked() error {
	if p.failed {
		return ErrUnavailable
	}
	p.state.Sequence++
	p.state.Time = time.Now()
	if err := Write(p.path, p.state); err != nil {
		p.failed = true
		// Never follow a replaced parent/snapshot to invalidate it.
		if dir, e := directory(p.path); e == nil {
			_ = removeSnapshot(dir)
			dir.Close()
		}
		if p.cancel != nil {
			p.cancel()
		}
		return ErrUnavailable
	}
	return nil
}
func (p *Publisher) Update(fn func(*Snapshot)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed {
		return
	}
	if fn != nil {
		fn(&p.state)
	}
	if p.probe != nil {
		p.state.Connected, p.state.Authenticated, p.state.Subscriptions, p.state.LocalReady = p.probe()
	}
	_ = p.publishLocked()
}
func (p *Publisher) Arm(probe func() (bool, bool, bool, bool)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probe = probe
	p.state.Armed = true
	p.state.Connected, p.state.Authenticated, p.state.Subscriptions, p.state.LocalReady = probe()
	_ = p.publishLocked()
}
func (p *Publisher) Registration(status string, credentials bool, err error) {
	p.Update(func(s *Snapshot) {
		s.RegistrationTime = time.Now()
		s.Credentials = credentials
		if err != nil {
			s.Registration = "error"
			return
		}
		switch status {
		case "healthy", "pending":
			s.Registration = status
		default:
			s.Registration = "unknown"
		}
	})
}
func (p *Publisher) Heartbeat(err error) {
	p.Update(func(s *Snapshot) {
		if err != nil {
			s.Failure = "heartbeat"
		} else {
			s.Heartbeat = time.Now()
			s.Failure = ""
		}
	})
}
