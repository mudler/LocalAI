// Package nibharness runs session chats in the nib agent harness. The chat's tools
// are served to nib over an in-process MCP transport; nib's own shell and
// file tools never start.
package nibharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Config is the model endpoint that every chat of a harness uses.
type Config struct {
	// LLM, when set, gives the model of a chat: its client, its name and its
	// context size. nib then uses that client and builds no provider of its own,
	// so the host can serve the model in process. Provider, BaseURL, APIKey and
	// Model are ignored.
	LLM             func(a workspace.Actor) (Model, error)
	Provider        string // empty: an OpenAI-compatible endpoint
	Model           string
	BaseURL         string
	APIKey          string
	APIKeyEnv       string
	ReasoningEffort string
	// ContextTokens is the context window of the model. Zero lets nib ask
	// the endpoint and fall back to 128000.
	ContextTokens int
	// LogLevel is the level of nib's process-wide logger: debug, info, warn or
	// error. Empty leaves the process logger as the caller configured it, so
	// the log level and format of the server apply. A value replaces the
	// process-wide logger, and the first call of New sets it. At debug, nib
	// logs tool arguments.
	LogLevel string
}

// Model is the model one chat talks to.
type Model struct {
	LLM  cogito.LLM
	Name string
	// ContextTokens is the model's context window in tokens. Zero uses the
	// harness's ContextTokens.
	ContextTokens int
}

// New returns a harness that starts one nib session per chat.
func New(cfg Config) session.Harness {
	if cfg.LogLevel != "" {
		logOnce.Do(func() {
			xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(cfg.LogLevel), "text"))
		})
	}
	return &harness{cfg: cfg}
}

// logOnce sets nib's logger once per process: xlog.SetLogger is not safe to
// call while a session runs, so the first New wins.
var logOnce sync.Once

type harness struct{ cfg Config }

// state is the resume document that Export writes.
type state struct {
	History   []openai.ChatCompletionMessage `json:"history"`
	Context   []openai.ChatCompletionMessage `json:"context"`
	Artifacts []types.Artifact               `json:"artifacts,omitempty"`
	Pruning   types.PruningState             `json:"pruning"`
}

func (h *harness) Start(ctx context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	// ctx is unused on purpose: the session outlives Start, so it runs on its
	// own context, cancelled by Close.
	_ = ctx
	if env.Dir == "" {
		return nil, errors.New("nib: no state folder")
	}
	if err := os.MkdirAll(env.Dir, 0o700); err != nil {
		return nil, err
	}
	fragments := []string{env.SystemPrompt}
	if env.TurnPrompt != "" {
		fragments = append(fragments, env.TurnPrompt)
	}
	// An empty BuiltinTools list means "every nib tool", including its shell, so
	// a chat with no host tools gets an allowlist that names nothing.
	names := []string{noBuiltinTools}
	var srv *toolServer
	var transports []mcp.Transport
	if len(env.Tools) > 0 {
		var tr mcp.Transport
		var err error
		if srv, tr, names, err = newToolServer(env.Tools); err != nil {
			return nil, err
		}
		transports = append(transports, tr)
	}
	model, ctxTokens := h.cfg.Model, h.cfg.ContextTokens
	var llm cogito.LLM
	if h.cfg.LLM != nil {
		m, err := h.cfg.LLM(env.Actor)
		if err != nil {
			return nil, fmt.Errorf("nib: model client: %w", err)
		}
		llm, model = m.LLM, m.Name
		if m.ContextTokens > 0 {
			ctxTokens = m.ContextTokens
		}
	}
	apiKey := h.cfg.APIKey
	if apiKey == "" && h.cfg.APIKeyEnv == "" {
		apiKey = "unused" // a local endpoint needs none; stop nib reading API_KEY from the host
	}
	// LoadWith fills every default that nib's own command line gets (retries,
	// compaction, pruning). Overrides win over the host environment.
	cfg := config.LoadWith(config.LoadOptions{
		BaseDir: filepath.Join(env.Dir, "nib"),
		// Without this, the host's MODEL, BASE_URL and API_KEY would retarget
		// the model and send a host key to the configured endpoint.
		SkipBareEnv: true,
		Overrides: types.Config{
			Provider:        h.cfg.Provider,
			Model:           model,
			BaseURL:         h.cfg.BaseURL,
			APIKey:          apiKey,
			APIKeyEnv:       h.cfg.APIKeyEnv,
			ReasoningEffort: h.cfg.ReasoningEffort,
			// The product text goes in a fragment: nib renders Prompt as a
			// template, and appends a fragment as it is.
			Prompt:          "You are an assistant that works on the user's files.",
			PromptFragments: fragments,
			BuiltinTools:    names,
			ApprovalMode:    types.ApprovalAuto,
			Compaction:      types.CompactionConfig{MaxContextTokens: ctxTokens},
		},
	})
	cfg.WorkingDir = env.Dir
	cfg.LLM = llm
	cfg.InputSource = env.Inputs
	cfg.LSPAutoDetect = new(bool)
	if len(env.Resume) > 0 {
		var st state
		if err := json.Unmarshal(env.Resume, &st); err != nil {
			return nil, fmt.Errorf("nib: bad resume state: %w", err)
		}
		cfg.InitialHistory, cfg.InitialContext = st.History, st.Context
		cfg.InitialArtifacts, cfg.InitialPruning = st.Artifacts, st.Pruning
	}
	hs := &hsession{emit: env.Emit, stop: func() {}}
	if srv != nil {
		hs.stop = srv.stop
	}
	sctx, cancel := context.WithCancel(context.Background())
	hs.cancel = cancel
	if srv != nil {
		srv.start(sctx)
	}
	var err error
	hs.sess, err = chat.NewSession(sctx, cfg, hs.callbacks(), transports...)
	if err != nil {
		cancel()
		return nil, err
	}
	return hs, nil
}

type hsession struct {
	sess   *chat.Session
	emit   func(session.Event)
	stop   func()
	cancel context.CancelFunc
	once   sync.Once
}

func (h *hsession) callbacks() chat.Callbacks {
	return chat.Callbacks{
		OnStream: func(ev chat.StreamEvent) {
			if ev.AgentID != "" || ev.Content == "" {
				return
			}
			switch ev.Kind {
			case "content":
				h.emit(session.Event{Kind: session.KindText, Text: ev.Content})
			case "reasoning":
				h.emit(session.Event{Kind: session.KindReasoning, Text: ev.Content})
			}
		},
		OnCompactDone: func(before, after int) {
			h.emit(session.Event{Kind: session.KindCompacted, Before: before, After: after})
		},
		OnToolStart: func(ts chat.ToolStart) {
			h.emit(session.Event{Kind: session.KindToolStart, Tool: &session.ToolEvent{
				Name: ts.Name, Summary: session.Summarize(json.RawMessage(ts.Arguments)),
			}})
		},
		OnToolResult: func(r chat.ToolResult) {
			h.emit(session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{
				Name: r.Name, Summary: session.Summarize(json.RawMessage(r.Arguments)),
				OK: toolOK(r), Output: session.CapText(r.Result, 2048),
			}})
		},
	}
}

func (h *hsession) Send(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			h.sess.Interrupt()
		case <-done:
		}
	}()
	_, err := h.sess.SendMessage(text)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (h *hsession) Inject(text string) error {
	if !h.sess.InjectUser(text) {
		return session.ErrNoTurn
	}
	return nil
}

func (h *hsession) Interrupt() error { h.sess.Interrupt(); return nil }

func (h *hsession) Export() ([]byte, error) {
	return json.Marshal(state{
		History:   h.sess.ExportHistory(),
		Context:   h.sess.ExportContext(),
		Artifacts: h.sess.ExportArtifacts(),
		Pruning:   h.sess.ExportPruning(),
	})
}

func (h *hsession) Close() error {
	var err error
	h.once.Do(func() {
		err = h.sess.Close()
		h.cancel()
		h.stop()
	})
	return err
}

// toolOK reports whether a finished tool call succeeded.
func toolOK(r chat.ToolResult) bool {
	if r.Outcome != "" && r.Outcome != "completed" {
		return false
	}
	failed, _ := chat.ToolOutcome(r.Result)
	return !failed
}

// noBuiltinTools is an allowlist entry that matches no nib tool, so a chat with
// no host tools enables none of nib's own.
const noBuiltinTools = "studio-no-builtin-tools"
