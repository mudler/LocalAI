// SPDX-License-Identifier: MIT
package runner

import (
	"context"
	"encoding/binary"
	"net"
	"net/url"
	"sync"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	pc "github.com/mudler/LocalAI/core/services/agentruntime/privatecheckpoint"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// localNativeFactory is deliberately limited to an explicitly supplied loopback
// fixture endpoint. It is not a production provider or tool bridge. Callers own
// an isolated temporary HOME and state directory. No caller Config is accepted.
type localNativeFactory struct {
	redacted
	endpoint, stateDir string
}

func denyNative(chat.ToolCallRequest) error { return errShell }
func (f localNativeFactory) config() (types.Config, error) {
	u, e := url.Parse(f.endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || f.stateDir == "" {
		return types.Config{}, errShell
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return types.Config{}, errShell
	}
	no := false
	return types.Config{Model: "fixture", BaseURL: f.endpoint, APIKey: "fixture-only", BaseDir: f.stateDir, WorkingDir: f.stateDir, IgnoreSavedEndpoint: true, LSPAutoDetect: &no, BuiltinTools: []string{"__runner_deny_all__"}, AgentOptions: types.AgentOptions{Iterations: 1, MaxAttempts: 1, MaxRetries: 1, NoTitles: true}, Compaction: types.CompactionConfig{Disabled: true, MaxContextTokens: 131072}}, nil
}
func (f localNativeFactory) New(ctx context.Context, id identity, p *pc.PrivatePayload, cb func()) (nativeSession, error) {
	if !id.valid() {
		return nil, errShell
	}
	cfg, e := f.config()
	if e != nil {
		return nil, errShell
	}
	if p != nil {
		cfg, e = nibadapter.RestoreConfig(cfg, p.NativeCheckpoint())
		if e != nil {
			return nil, errShell
		}
	}
	life, cancel := context.WithCancel(ctx)
	// OnStream remains nil: the non-streaming call has no relay goroutine.
	// Status callbacks carry no provider text into the shell.
	s, e := chat.NewSession(life, cfg, chat.Callbacks{ToolPolicy: denyNative, OnStatus: func(string) { cb() }})
	if e != nil {
		cancel()
		return nil, errShell
	}
	return &localNativeSession{session: s, cancel: cancel, done: make(chan struct{})}, nil
}

type localNativeSession struct {
	redacted
	mu                      sync.Mutex
	session                 *chat.Session
	cancel                  context.CancelFunc
	done                    chan struct{}
	started, joined, closed bool
}

func (s *localNativeSession) Run(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.started || s.joined || s.closed || ctx.Err() != nil {
		s.mu.Unlock()
		return errShell
	}
	s.started = true
	s.mu.Unlock()
	defer close(s.done)
	_, e := s.session.SendMessage(string(input))
	if e != nil {
		return errShell
	}
	return nil
}
func (s *localNativeSession) Cancel() { s.cancel(); s.session.Interrupt() }
func (s *localNativeSession) AwaitQuiescence(ctx context.Context) error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if started {
		select {
		case <-s.done:
		case <-ctx.Done():
			return errShell
		}
	}
	// The only unconditional SendMessage goroutine is the stale-agent watcher.
	// With no agents it reads the empty agent log; Inject cannot mutate exported
	// sections. It is cancelled by SendMessage's defer. No stream relay, skills,
	// transports, titles or helpers exist in this adapter. Joining SendMessage
	// therefore joins ALL checkpoint writers, not every inert nib goroutine.
	if s.session.RunLive() || s.session.AgentManager().HasRunning() {
		return errShell
	}
	s.mu.Lock()
	s.joined = true
	s.mu.Unlock()
	return nil
}
func (s *localNativeSession) Export() (nibadapter.Checkpoint, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.joined || s.closed {
		return nibadapter.Checkpoint{}, nil, errShell
	}
	cp, e := nibadapter.ExportCheckpoint(s.session, true)
	if e != nil {
		return nibadapter.Checkpoint{}, nil, errShell
	}
	cursor := make([]byte, 8)
	binary.BigEndian.PutUint64(cursor, uint64(len(s.session.ExportHistory())))
	return cp, cursor, nil
}
func (s *localNativeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.joined {
		return errShell
	}
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.session.Close() != nil {
		return errShell
	}
	return nil
}
