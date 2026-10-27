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
}

// AgentOptions shape the tool loop of one chat. Zero values keep nib's own
// defaults.
type AgentOptions struct {
	// Iterations caps the tool rounds of one turn.
	Iterations int
	// MaxAttempts is how often a failed tool call is retried.
	MaxAttempts int
	// ForceReasoning makes the model reason before it picks a tool.
	ForceReasoning bool
}

// Model is the model one chat talks to.
type Model struct {
	LLM  cogito.LLM
	Name string
	// ContextTokens is the model's context window in tokens. Zero uses the
	// harness's ContextTokens.
	ContextTokens int
	// ReasoningEffort overrides the harness's own setting for this chat. Empty:
	// the harness's setting.
	ReasoningEffort string
	// Agent sets the options of the tool loop of the model.
	Agent AgentOptions
}

// New returns a harness that starts one nib session per chat.
func New(cfg Config) session.Harness {
	return &harness{cfg: cfg}
}

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
	model, ctxTokens, effort := h.cfg.Model, h.cfg.ContextTokens, h.cfg.ReasoningEffort
	var agentOpts AgentOptions
	var llm cogito.LLM
	if h.cfg.LLM != nil {
		m, err := h.cfg.LLM(env.Actor)
		if err != nil {
			return nil, fmt.Errorf("nib: model client: %w", err)
		}
		llm, model, agentOpts = m.LLM, m.Name, m.Agent
		hostNames := make([]string, len(env.Tools))
		for i, t := range env.Tools {
			hostNames[i] = t.Name
		}
		llm = withAliases(llm, hostNames)
		if m.ContextTokens > 0 {
			ctxTokens = m.ContextTokens
		}
		if m.ReasoningEffort != "" {
			effort = m.ReasoningEffort
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
			ReasoningEffort: effort,
			// The product text goes in a fragment: nib renders Prompt as a
			// template, and appends a fragment as it is.
			Prompt:          "You are an assistant that works on the user's files.",
			PromptFragments: fragments,
			BuiltinTools:    names,
			ApprovalMode:    types.ApprovalAuto,
			Compaction:      types.CompactionConfig{MaxContextTokens: ctxTokens},
		},
	})
	applyAgentOptions(&cfg, agentOpts)
	cfg.WorkingDir = env.Dir
	// The host lists its own skills in the prompt; nib's (about-nib, and the
	// load_skill tool that reads them) do not apply to a chat.
	cfg.Skills = nil
	if llm != nil {
		llm = forHost(llm)
	}
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

// applyAgentOptions copies the options a model sets into nib's config. A zero
// value keeps what LoadWith filled in.
func applyAgentOptions(cfg *types.Config, o AgentOptions) {
	if o.Iterations > 0 {
		cfg.AgentOptions.Iterations = o.Iterations
	}
	if o.MaxAttempts > 0 {
		cfg.AgentOptions.MaxAttempts = o.MaxAttempts
	}
	if o.ForceReasoning {
		cfg.AgentOptions.ForceReasoning = true
	}
}

type hsession struct {
	sess   *chat.Session
	emit   func(session.Event)
	stop   func()
	cancel context.CancelFunc
	once   sync.Once

	thinkMu sync.Mutex
	think   thinkSplitter
}

// emitAnswer sends a chunk of the answer, with inline <think> blocks told
// apart as reasoning. Chunks arrive in order from one stream.
func (h *hsession) emitAnswer(chunk string) {
	h.thinkMu.Lock()
	pieces := h.think.feed(chunk)
	h.thinkMu.Unlock()
	h.emitPieces(pieces)
}

// flushAnswer sends what the splitter held back at the end of a turn.
func (h *hsession) flushAnswer() {
	h.thinkMu.Lock()
	pieces := h.think.flush()
	h.thinkMu.Unlock()
	h.emitPieces(pieces)
}

func (h *hsession) emitPieces(pieces []piece) {
	for _, p := range pieces {
		kind := session.KindText
		if p.reasoning {
			kind = session.KindReasoning
		}
		h.emit(session.Event{Kind: kind, Text: p.text})
	}
}

func (h *hsession) callbacks() chat.Callbacks {
	return chat.Callbacks{
		OnStream: func(ev chat.StreamEvent) {
			if ev.AgentID != "" || ev.Content == "" {
				return
			}
			switch ev.Kind {
			case "content":
				h.emitAnswer(ev.Content)
			case "reasoning":
				h.emit(session.Event{Kind: session.KindReasoning, Text: ev.Content})
			}
		},
		OnCompactDone: func(before, after int) {
			h.emit(session.Event{Kind: session.KindCompacted, Before: before, After: after})
		},
		OnToolStart: func(ts chat.ToolStart) {
			h.emit(session.Event{Kind: session.KindToolStart, Tool: &session.ToolEvent{
				Name: hostName(ts.Name), Summary: session.Summarize(json.RawMessage(ts.Arguments)),
			}})
		},
		OnToolResult: func(r chat.ToolResult) {
			h.emit(session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{
				Name: hostName(r.Name), Summary: session.Summarize(json.RawMessage(r.Arguments)),
				OK: toolOK(r), Output: session.CapText(r.Result, 2048),
			}})
		},
	}
}

func (h *hsession) Send(ctx context.Context, text string) error {
	return h.send(ctx, text, nil)
}

// SendMedia implements session.MediaSession: the images and videos go to the
// model as parts of the user's message.
func (h *hsession) SendMedia(ctx context.Context, text string, media []session.Media) error {
	parts := make([]chat.ContentPart, 0, len(media))
	for _, m := range media {
		kind := chat.PartImage
		if m.Kind == session.MediaVideo {
			kind = chat.PartVideo
		}
		parts = append(parts, chat.ContentPart{Kind: kind, DataURI: m.DataURI})
	}
	return h.send(ctx, text, parts)
}

func (h *hsession) send(ctx context.Context, text string, parts []chat.ContentPart) error {
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
	_, err := h.sess.SendMessage(text, parts...)
	h.flushAnswer()
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

// TranscriptState builds the resume document of a chat from the visible
// messages of its transcript: the user's and the assistant's. The tool calls
// and the images are not rebuilt. It is what a chat gets after its transcript
// was cut for a regenerate, an edit or a fork, so the model sees the part that
// stays and nothing of the part that went.
func TranscriptState(entries []session.Entry) ([]byte, error) {
	var msgs []openai.ChatCompletionMessage
	for _, e := range entries {
		switch e.Role {
		case session.RoleUser:
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: e.Text})
		case session.RoleAssistant:
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: e.Text})
		}
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return json.Marshal(state{History: msgs, Context: msgs})
}
