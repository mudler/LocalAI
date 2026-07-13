package types

import (
	"encoding/json"
	"fmt"
)

// ClassifierConfig is a LocalAI extension to the Realtime API
// (session.localai_classifier, response.localai_classifier): instead of
// autoregressive generation, each user turn is prefill-scored against a
// fixed option list via the Score primitive and the winning option's canned
// reply / tool call is emitted. Built for hardware that can afford prefill
// but not decode (e.g. a Raspberry Pi running a small LLM).
type ClassifierConfig struct {
	// Enabled is a pointer so a response-level override can force
	// classification off for one response ({"enabled": false}) without
	// replacing the session's option list. nil means "on when options
	// exist".
	Enabled *bool `json:"enabled,omitempty"`

	// Options the user turn is scored against. Replaced wholesale by
	// session.update / response.create, like tools.
	Options []ClassifierOption `json:"options,omitempty"`

	// Threshold is the softmax-probability floor the best option must
	// clear; below it the fallback applies. 0 always picks the argmax.
	Threshold float64 `json:"threshold,omitempty"`

	// Normalization selects how candidate log-probs are compared before
	// the softmax: "raw" (default, joint log-prob) or "mean"
	// (length-normalized) — same semantics as the router's
	// score_normalization.
	Normalization string `json:"normalization,omitempty"`

	// HistoryItems selects what gets scored. 0 (default) and -1 score
	// only the latest user message; a positive N includes the trailing N
	// conversation messages, role-labeled. Prior turns echo option names
	// (the canned replies especially) and empirically dominate small
	// scoring models — only opt into history with a scorer large enough
	// to weigh it.
	HistoryItems int `json:"history_items,omitempty"`

	// Fallback controls what happens when no option clears the
	// threshold. nil behaves like {"mode": "none"}.
	Fallback *ClassifierFallback `json:"fallback,omitempty"`

	// Address, when set, gates every turn on the assistant being
	// addressed by name ("Drone go up", not just "go up") — the
	// wake-word pattern. The check is a deterministic word match on the
	// transcript: scoring cannot do it (a 1.2B scorer rates "go up" as
	// addressed=1.0 even with a dedicated addressing stage) and matching
	// is free, so unaddressed ambient speech skips scoring entirely.
	Address *ClassifierAddress `json:"address,omitempty"`
}

// ClassifierAddress configures name-gating for classifier mode.
type ClassifierAddress struct {
	// Names that count as addressing the assistant, matched as
	// case-insensitive whole words against the latest user turn.
	Names []string `json:"names"`

	// Mode when the turn does not mention a name: "ignore" (default —
	// the response completes silently, the right behavior for ambient
	// conversation) or "reply" (speak Reply).
	Mode string `json:"mode,omitempty"`

	// Reply spoken in "reply" mode.
	Reply string `json:"reply,omitempty"`
}

// Address gate modes.
const (
	ClassifierAddressIgnore = "ignore"
	ClassifierAddressReply  = "reply"
)

// ClassifierNotAddressed is the ClassifierResultEvent.Fallback value for
// turns dropped by the address gate. It is an event-only value — the
// config fallback modes stay none|reply|generate.
const ClassifierNotAddressed = "not_addressed"

// AddressMode returns the effective address-gate mode.
func (a *ClassifierAddress) AddressMode() string {
	if a == nil || a.Mode == "" {
		return ClassifierAddressIgnore
	}
	return a.Mode
}

// ClassifierOption is one selectable intent: what to match on
// (Description), what to say when chosen (Reply) and, optionally, a canned
// tool call the client executes.
type ClassifierOption struct {
	// ID identifies the option in results and doubles as the scored
	// route label, so keep it short — its tokens are what the model
	// actually scores.
	ID string `json:"id"`

	// Description tells the model when the option applies (e.g. "the
	// user asks the drone to move or fly up/higher"). It goes into the
	// classification system prompt.
	Description string `json:"description"`

	// Reply is the canned assistant reply spoken/emitted when the
	// option wins. Empty means the option is silent (tool-only).
	Reply string `json:"reply,omitempty"`

	// Tool, when set, is emitted as a function_call item with these
	// exact arguments when the option wins.
	Tool *ClassifierTool `json:"tool,omitempty"`
}

// ClassifierTool is a canned function call. Arguments stays a raw JSON
// object so future slot-filling (templated arguments) is an additive
// change.
type ClassifierTool struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Classifier fallback modes.
const (
	// ClassifierFallbackNone completes the response with no output.
	ClassifierFallbackNone = "none"
	// ClassifierFallbackReply speaks/emits the canned fallback reply.
	ClassifierFallbackReply = "reply"
	// ClassifierFallbackGenerate falls through to normal autoregressive
	// generation for that response.
	ClassifierFallbackGenerate = "generate"
)

// ClassifierFallback selects the below-threshold behavior.
type ClassifierFallback struct {
	Mode  string `json:"mode,omitempty"`
	Reply string `json:"reply,omitempty"`
}

// Active reports whether classification should run: explicitly enabled, or
// enabled by default because options are present.
func (c *ClassifierConfig) Active() bool {
	if c == nil {
		return false
	}
	if c.Enabled != nil {
		return *c.Enabled && len(c.Options) > 0
	}
	return len(c.Options) > 0
}

// FallbackMode returns the effective fallback mode.
func (c *ClassifierConfig) FallbackMode() string {
	if c == nil || c.Fallback == nil || c.Fallback.Mode == "" {
		return ClassifierFallbackNone
	}
	return c.Fallback.Mode
}

// Validate checks the invariants the scoring engine relies on. It is
// shared by the session.update path and pipeline-config seeding so both
// reject bad option lists the same way.
func (c *ClassifierConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.Threshold < 0 || c.Threshold >= 1 {
		return fmt.Errorf("classifier: threshold must be in [0,1), got %v", c.Threshold)
	}
	switch c.Normalization {
	case "", "raw", "mean":
	default:
		return fmt.Errorf("classifier: normalization must be \"raw\" or \"mean\", got %q", c.Normalization)
	}
	if c.HistoryItems < -1 {
		return fmt.Errorf("classifier: history_items must be >= -1, got %d", c.HistoryItems)
	}
	switch c.FallbackMode() {
	case ClassifierFallbackNone, ClassifierFallbackReply, ClassifierFallbackGenerate:
	default:
		return fmt.Errorf("classifier: fallback mode must be one of none|reply|generate, got %q", c.Fallback.Mode)
	}
	if c.FallbackMode() == ClassifierFallbackReply && (c.Fallback == nil || c.Fallback.Reply == "") {
		return fmt.Errorf("classifier: fallback mode \"reply\" requires a non-empty fallback reply")
	}
	if c.Address != nil {
		named := false
		for _, n := range c.Address.Names {
			if n != "" {
				named = true
				break
			}
		}
		if !named {
			return fmt.Errorf("classifier: address gate requires at least one non-empty name")
		}
		switch c.Address.AddressMode() {
		case ClassifierAddressIgnore, ClassifierAddressReply:
		default:
			return fmt.Errorf("classifier: address mode must be one of ignore|reply, got %q", c.Address.Mode)
		}
		if c.Address.AddressMode() == ClassifierAddressReply && c.Address.Reply == "" {
			return fmt.Errorf("classifier: address mode \"reply\" requires a non-empty reply")
		}
	}
	seen := make(map[string]struct{}, len(c.Options))
	for i, opt := range c.Options {
		if opt.ID == "" {
			return fmt.Errorf("classifier: option %d has an empty id", i)
		}
		if _, dup := seen[opt.ID]; dup {
			return fmt.Errorf("classifier: duplicate option id %q", opt.ID)
		}
		seen[opt.ID] = struct{}{}
		if opt.Description == "" {
			return fmt.Errorf("classifier: option %q has an empty description", opt.ID)
		}
		if opt.Tool != nil {
			if opt.Tool.Name == "" {
				return fmt.Errorf("classifier: option %q has a tool with an empty name", opt.ID)
			}
			if len(opt.Tool.Arguments) > 0 {
				var obj map[string]any
				if err := json.Unmarshal(opt.Tool.Arguments, &obj); err != nil {
					return fmt.Errorf("classifier: option %q tool arguments must be a JSON object: %w", opt.ID, err)
				}
			}
		}
	}
	return nil
}

// ClassifierScore is one entry of the softmax distribution over options.
type ClassifierScore struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// ClassifierResultEvent is a LocalAI extension server event
// (localai.classifier.result) emitted once per classifier-handled response
// — including fallbacks — before the output items, so clients can
// visualize the decision and its confidence.
type ClassifierResultEvent struct {
	ServerEventBase

	// The ID of the response this classification belongs to.
	ResponseID string `json:"response_id"`

	// The full softmax distribution, in option-declaration order.
	Scores []ClassifierScore `json:"scores"`

	// The winning option id, or "" when the fallback applied.
	ChosenID string `json:"chosen_id,omitempty"`

	// The threshold the winner had to clear.
	Threshold float64 `json:"threshold"`

	// The fallback mode that applied, or "" when an option was chosen.
	Fallback string `json:"fallback,omitempty"`

	// Wall-clock scoring latency.
	LatencyMs int64 `json:"latency_ms"`
}

func (m ClassifierResultEvent) ServerEventType() ServerEventType {
	return ServerEventTypeClassifierResult
}

func (m ClassifierResultEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ClassifierResultEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}
