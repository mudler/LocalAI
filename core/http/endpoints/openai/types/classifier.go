package types

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// ClassifierTool is a canned function call. Arguments is a raw JSON
// object; with Slots it becomes a template whose "{{name}}" placeholders
// are filled by a short constrained completion after classification —
// the hybrid between prefill-only classification and full generation.
type ClassifierTool struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	// Slots declares the argument holes to fill by inference when the
	// option wins. Number slots substitute the quoted placeholder
	// ("{{name}}" -> 3.5) so YAML/JSON templates stay well-formed; enum
	// and string slots substitute inside their quotes.
	Slots []ClassifierSlot `json:"slots,omitempty"`
}

// Classifier slot types.
const (
	ClassifierSlotNumber = "number"
	ClassifierSlotEnum   = "enum"
	ClassifierSlotString = "string"
)

// ClassifierSlot is one inferred argument of a classifier tool call.
type ClassifierSlot struct {
	// Name of the slot; "{{name}}" in the arguments template marks where
	// its value lands, and the model sees it as a JSON field name.
	Name string `json:"name"`

	// Type constrains the completion grammar: "number", "enum" or
	// "string".
	Type string `json:"type"`

	// Values enumerates the admissible values for enum slots.
	Values []string `json:"values,omitempty"`

	// Default applies when inference fails outright. Enum defaults must
	// be one of Values; number defaults must parse as a number. A slot
	// without a default makes the whole response fall back on failure.
	Default string `json:"default,omitempty"`

	// Hint is appended to the option's description in the scoring/fill
	// system prompt (e.g. "assume meters when the user gives no units").
	Hint string `json:"hint,omitempty"`
}

// slotPlaceholder returns the template marker for a slot.
func slotPlaceholder(name string) string { return "{{" + name + "}}" }

// SampleValue returns a syntactically valid stand-in for template
// validation: the default when set, otherwise a type-appropriate value.
func (s *ClassifierSlot) SampleValue() string {
	if s.Default != "" {
		return s.Default
	}
	switch s.Type {
	case ClassifierSlotNumber:
		return "0"
	case ClassifierSlotEnum:
		if len(s.Values) > 0 {
			return s.Values[0]
		}
	}
	return "sample"
}

// SpliceArguments fills the tool's argument template with the given slot
// values and returns the final JSON arguments string. Number values
// replace the quoted placeholder so they land unquoted; other types are
// JSON-string-escaped in place. The result must parse as a JSON object.
func (t *ClassifierTool) SpliceArguments(values map[string]string) (string, error) {
	args := "{}"
	if len(t.Arguments) > 0 {
		args = string(t.Arguments)
	}
	for i := range t.Slots {
		s := &t.Slots[i]
		v, ok := values[s.Name]
		if !ok || v == "" {
			return "", fmt.Errorf("classifier: no value for slot %q", s.Name)
		}
		ph := slotPlaceholder(s.Name)
		if s.Type == ClassifierSlotNumber {
			args = strings.ReplaceAll(args, `"`+ph+`"`, v)
		} else {
			esc, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			args = strings.ReplaceAll(args, ph, string(esc[1:len(esc)-1]))
		}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil {
		return "", fmt.Errorf("classifier: spliced tool arguments are not a JSON object: %w", err)
	}
	return args, nil
}

// SpliceReply fills "{{name}}" placeholders in the option's spoken reply
// with the same slot values that filled the tool arguments, as plain text
// ("Going {{distance}} {{units}}." → "Going 3 meters."), so the reply can
// confirm what was actually inferred. Values are optional in the reply:
// placeholders without a value stay literal, and options without slots (or
// a nil value set) return the reply verbatim.
func (o *ClassifierOption) SpliceReply(values map[string]string) string {
	reply := o.Reply
	if o.Tool == nil || len(values) == 0 {
		return reply
	}
	for i := range o.Tool.Slots {
		s := &o.Tool.Slots[i]
		if v, ok := values[s.Name]; ok && v != "" {
			reply = strings.ReplaceAll(reply, slotPlaceholder(s.Name), v)
		}
	}
	return reply
}

// SlotDefaults returns every slot's default value, or an error naming the
// first slot without one — the fill-failure path either recovers with a
// complete default set or not at all.
func (t *ClassifierTool) SlotDefaults() (map[string]string, error) {
	values := make(map[string]string, len(t.Slots))
	for i := range t.Slots {
		if t.Slots[i].Default == "" {
			return nil, fmt.Errorf("classifier: slot %q has no default", t.Slots[i].Name)
		}
		values[t.Slots[i].Name] = t.Slots[i].Default
	}
	return values, nil
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
			if len(opt.Tool.Arguments) > 0 && len(opt.Tool.Slots) == 0 {
				var obj map[string]any
				if err := json.Unmarshal(opt.Tool.Arguments, &obj); err != nil {
					return fmt.Errorf("classifier: option %q tool arguments must be a JSON object: %w", opt.ID, err)
				}
			}
			if err := validateSlots(opt.Tool); err != nil {
				return fmt.Errorf("classifier: option %q: %w", opt.ID, err)
			}
		}
	}
	return nil
}

// slotNamePattern keeps slot names safe to embed as JSON field names and
// template placeholders without escaping.
var slotNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateSlots(t *ClassifierTool) error {
	if len(t.Slots) == 0 {
		return nil
	}
	args := string(t.Arguments)
	seen := make(map[string]struct{}, len(t.Slots))
	sample := make(map[string]string, len(t.Slots))
	for i := range t.Slots {
		s := &t.Slots[i]
		if !slotNamePattern.MatchString(s.Name) {
			return fmt.Errorf("slot %d has invalid name %q", i, s.Name)
		}
		if _, dup := seen[s.Name]; dup {
			return fmt.Errorf("duplicate slot %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		switch s.Type {
		case ClassifierSlotNumber:
			if s.Default != "" {
				if _, err := strconv.ParseFloat(s.Default, 64); err != nil {
					return fmt.Errorf("slot %q: number default %q does not parse", s.Name, s.Default)
				}
			}
		case ClassifierSlotEnum:
			if len(s.Values) == 0 {
				return fmt.Errorf("slot %q: enum slots need values", s.Name)
			}
			if slices.Contains(s.Values, "") {
				return fmt.Errorf("slot %q: enum values must be non-empty", s.Name)
			}
			if s.Default != "" && !slices.Contains(s.Values, s.Default) {
				return fmt.Errorf("slot %q: default %q is not one of its values", s.Name, s.Default)
			}
		case ClassifierSlotString:
		default:
			return fmt.Errorf("slot %q: type must be one of number|enum|string, got %q", s.Name, s.Type)
		}
		if !strings.Contains(args, slotPlaceholder(s.Name)) {
			return fmt.Errorf("slot %q: arguments template does not reference {{%s}}", s.Name, s.Name)
		}
		sample[s.Name] = s.SampleValue()
	}
	// The template with type-appropriate values must produce a JSON
	// object, catching e.g. an unquoted string placeholder up front.
	if _, err := t.SpliceArguments(sample); err != nil {
		return fmt.Errorf("arguments template does not splice: %w", err)
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

	// The chosen option's final tool arguments when its slots were filled
	// by inference (the hybrid classify-then-complete path).
	Arguments string `json:"arguments,omitempty"`

	// Wall-clock slot-fill latency; zero when the option has no slots.
	FillLatencyMs int64 `json:"fill_latency_ms,omitempty"`
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
