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
	open func() (string, string)
}

func newLocalNativeFactory(endpoint, stateDir string) localNativeFactory {
	return localNativeFactory{open: func() (string, string) { return endpoint, stateDir }}
}
func denyNative(chat.ToolCallRequest) error { return errShell }
func (f localNativeFactory) config() (types.Config, error) {
	if f.open == nil {
		return types.Config{}, errShell
	}
	endpoint, stateDir := f.open()
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || stateDir == "" {
		return types.Config{}, errShell
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return types.Config{}, errShell
	}
	no := false
	return types.Config{Model: "fixture", BaseURL: endpoint, APIKey: "fixture-only", BaseDir: stateDir, WorkingDir: stateDir, IgnoreSavedEndpoint: true, LSPAutoDetect: &no, BuiltinTools: []string{"__runner_deny_all__"}, AgentOptions: types.AgentOptions{Iterations: 1, MaxAttempts: 1, MaxRetries: 1, NoTitles: true}, Compaction: types.CompactionConfig{Disabled: true, MaxContextTokens: 131072}}, nil
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
	fields := &localNativeFields{session: s, cancel: cancel, done: make(chan struct{})}
	return &localNativeSession{open: func() *localNativeFields { return fields }}, nil
}

type localNativeSession struct {
	redacted
	open func() *localNativeFields
}

func (s *localNativeSession) fields() *localNativeFields { return s.open() }

type localNativeFields struct {
	mu                                 sync.Mutex
	session                            *chat.Session
	cancel                             context.CancelFunc
	done                               chan struct{}
	started, joined, closed, cancelled bool
}

func (s *localNativeSession) Run(ctx context.Context, input []byte, permit *startPermit) error {
	s.fields().mu.Lock()
	if s.fields().cancelled || permit == nil || !permit.enter(ctx) || s.fields().started || s.fields().joined || s.fields().closed || ctx.Err() != nil {
		s.fields().mu.Unlock()
		return errShell
	}
	s.fields().started = true
	s.fields().mu.Unlock()
	defer close(s.fields().done)
	_, e := s.fields().session.SendMessage(string(input))
	if e != nil {
		return errShell
	}
	return nil
}
func (s *localNativeSession) Cancel() {
	s.fields().mu.Lock()
	s.fields().cancelled = true
	s.fields().cancel()
	s.fields().mu.Unlock()
	s.fields().session.Interrupt()
}
func (s *localNativeSession) AwaitQuiescence(ctx context.Context) error {
	s.fields().mu.Lock()
	started := s.fields().started
	if !started {
		s.fields().joined = true
	}
	s.fields().mu.Unlock()
	if started {
		select {
		case <-s.fields().done:
		case <-ctx.Done():
			return errShell
		}
	}
	// The only unconditional SendMessage goroutine is the stale-agent watcher.
	// With no agents it reads the empty agent log; Inject cannot mutate exported
	// sections. It is cancelled by SendMessage's defer. No stream relay, skills,
	// transports, titles or helpers exist in this adapter. Joining SendMessage
	// therefore joins ALL checkpoint writers, not every inert nib goroutine.
	if s.fields().session.RunLive() || s.fields().session.AgentManager().HasRunning() {
		return errShell
	}
	s.fields().mu.Lock()
	s.fields().joined = true
	s.fields().mu.Unlock()
	return nil
}
func (s *localNativeSession) Export() (nibadapter.Checkpoint, []byte, error) {
	s.fields().mu.Lock()
	defer s.fields().mu.Unlock()
	if !s.fields().joined || s.fields().closed {
		return nibadapter.Checkpoint{}, nil, errShell
	}
	cp, e := nibadapter.ExportCheckpoint(s.fields().session, true)
	if e != nil {
		return nibadapter.Checkpoint{}, nil, errShell
	}
	cursor := make([]byte, 8)
	binary.BigEndian.PutUint64(cursor, uint64(len(s.fields().session.ExportHistory())))
	return cp, cursor, nil
}
func (s *localNativeSession) Close() error {
	s.fields().mu.Lock()
	defer s.fields().mu.Unlock()
	if !s.fields().joined {
		return errShell
	}
	if s.fields().closed {
		return nil
	}
	s.fields().closed = true
	s.fields().cancel()
	if s.fields().session.Close() != nil {
		return errShell
	}
	return nil
}
