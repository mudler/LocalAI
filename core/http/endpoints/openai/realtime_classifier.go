package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/xlog"
)

// Classifier mode (LocalAI extension): instead of autoregressive
// generation, each user turn is prefill-scored against a registered option
// list via the Score primitive and the winning option's canned reply /
// tool call is emitted. Designed for hardware that can afford prefill but
// not decode. See docs/content/features/openai-realtime.md.

// By default only the latest user message is scored. Earlier turns in the
// probe — the assistant's canned replies especially — echo option names
// ("Going up." ↔ up) and verified empirically to dominate small scoring
// models: with any prior turn present, a 1.2B model kept re-choosing the
// previous option at p≈1.0 regardless of the new command. history_items > 0
// opts back into context (role-labeled), for larger scoring models.

// classifierConfigFromPipeline converts the YAML pipeline.classifier block
// into the wire ClassifierConfig and validates it, so a bad option list
// rejects the session at setup rather than misbehaving on the first turn.
// A nil block yields a nil config (classifier off).
func classifierConfigFromPipeline(p *config.PipelineClassifier) (*types.ClassifierConfig, error) {
	if p == nil {
		return nil, nil
	}
	cc := &types.ClassifierConfig{
		Enabled:       &p.Enabled,
		Threshold:     p.Threshold,
		Normalization: p.Normalization,
		HistoryItems:  p.HistoryItems,
	}
	if p.Fallback != nil {
		cc.Fallback = &types.ClassifierFallback{Mode: p.Fallback.Mode, Reply: p.Fallback.Reply}
	}
	if p.Address != nil {
		cc.Address = &types.ClassifierAddress{Names: p.Address.Names, Mode: p.Address.Mode, Reply: p.Address.Reply}
	}
	for _, o := range p.Options {
		opt := types.ClassifierOption{
			ID:          o.ID,
			Description: o.Description,
			Reply:       o.Reply,
		}
		if o.Tool != nil {
			args := json.RawMessage(nil)
			if o.Tool.Arguments != nil {
				data, err := json.Marshal(o.Tool.Arguments)
				if err != nil {
					return nil, fmt.Errorf("option %q: marshal tool arguments: %w", o.ID, err)
				}
				args = data
			}
			opt.Tool = &types.ClassifierTool{Name: o.Tool.Name, Arguments: args}
			for _, s := range o.Tool.Slots {
				opt.Tool.Slots = append(opt.Tool.Slots, types.ClassifierSlot{
					Name:    s.Name,
					Type:    s.Type,
					Values:  s.Values,
					Default: s.Default,
					Hint:    s.Hint,
				})
			}
		}
		cc.Options = append(cc.Options, opt)
	}
	if err := cc.Validate(); err != nil {
		return nil, err
	}
	return cc, nil
}

// resolveClassifier merges the session classifier config with a
// response-level override: a non-nil override replaces the whole block
// (same replace-not-merge semantics as tools), so {"enabled": false} runs
// normal generation for one response.
func resolveClassifier(sessionCfg *types.ClassifierConfig, overrides *types.ResponseCreateParams) *types.ClassifierConfig {
	if overrides != nil && overrides.LocalAIClassifier != nil {
		return overrides.LocalAIClassifier
	}
	return sessionCfg
}

// validateClassifierActivation verifies both the wire config and the concrete
// backend selected to score it. Scoring capacity is reserved at model load
// only for configs that explicitly declare the score usecase, so accepting an
// active classifier on any other model would defer a deterministic failure to
// the first response.
func validateClassifierActivation(m Model, cc *types.ClassifierConfig) error {
	if cc == nil {
		return nil
	}
	if err := cc.Validate(); err != nil {
		return err
	}
	if !cc.Active() {
		return nil
	}
	wm, ok := m.(*wrappedModel)
	if !ok {
		return fmt.Errorf("classifier: the session model does not support scoring")
	}
	cfg := wm.scoreConfig()
	if cfg == nil || !cfg.HasUsecases(config.FLAG_SCORE) {
		name := ""
		if cfg != nil {
			name = cfg.Name
		}
		return fmt.Errorf("classifier: scoring model %q must declare known_usecases: [score]", name)
	}
	if cfg.HasRouter() {
		return fmt.Errorf("classifier: scoring model %q is a router; configure a concrete pipeline.classifier.model", cfg.Name)
	}
	return nil
}

// trimClassifierHistory drops system messages (the classifier builds its
// own option-list system prompt) and selects what gets scored.
// historyItems <= 0 (the default): only the latest user message. Positive
// N: the trailing N conversation messages.
func trimClassifierHistory(history schema.Messages, historyItems int) schema.Messages {
	conversation := make(schema.Messages, 0, len(history))
	for _, m := range history {
		if m.Role == string(types.MessageRoleSystem) {
			continue
		}
		conversation = append(conversation, m)
	}
	if historyItems <= 0 {
		for i := len(conversation) - 1; i >= 0; i-- {
			if conversation[i].Role == string(types.MessageRoleUser) {
				return conversation[i : i+1]
			}
		}
		return nil
	}
	if len(conversation) > historyItems {
		conversation = conversation[len(conversation)-historyItems:]
	}
	return conversation
}

// latestUserText returns the text of the most recent user message — the
// turn the address gate inspects (earlier turns being addressed doesn't
// make this one addressed).
func latestUserText(messages schema.Messages) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == string(types.MessageRoleUser) {
			text, _ := messages[i].Content.(string)
			return text
		}
	}
	return ""
}

// mentionsAnyName reports whether text contains any of the names as a
// case-insensitive whole word ("drone" matches "Drone, go up" but not
// "drones").
func mentionsAnyName(text string, names []string) bool {
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(n) + `\b`)
		if err != nil {
			continue
		}
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// classifierProbe renders the trimmed history for scoring. A single user
// message goes in verbatim — that matches the scoring format's training
// distribution (Arch-Router scores "the user's request"). When
// history_items opts extra turns in, every line carries a role label so
// the scoring model can at least tell the user's request apart from the
// assistant's replies.
func classifierProbe(messages schema.Messages) router.Probe {
	parts := make([]string, 0, len(messages))
	label := len(messages) > 1
	for _, msg := range messages {
		text, _ := msg.Content.(string)
		if text == "" {
			continue // e.g. tool-call items carry no text
		}
		if label {
			switch msg.Role {
			case string(types.MessageRoleAssistant):
				text = "Assistant: " + text
			case "tool":
				text = "Tool: " + text
			default:
				text = "User: " + text
			}
		}
		parts = append(parts, text)
	}
	return router.Probe{Prompt: router.JoinTurns(parts), Messages: parts}
}

// classifierRespond runs one classifier-mode response: score the options,
// emit the localai.classifier.result observability event, then either the
// winning option's canned reply/tool, the fallback reply, nothing, or —
// for the generate fallback — report false so the caller falls through to
// normal generation. Runs inside the respcoord-issued response body, so
// the single terminal stays owned by triggerResponse. Returns true when
// the response was fully handled here.
func classifierRespond(ctx context.Context, session *Session, conv *Conversation, t Transport, r *liveResponse, cc *types.ClassifierConfig, history schema.Messages, overrides *types.ResponseCreateParams, toolTurn int) bool {
	msgs := trimClassifierHistory(history, cc.HistoryItems)
	if len(msgs) == 0 {
		xlog.Debug("realtime classifier: no scorable conversation content; skipping to generation")
		return false
	}

	// Address gate (wake-word behavior): when configured, a turn that
	// doesn't mention one of the assistant's names is dropped before any
	// scoring — the check is a deterministic word match on the transcript
	// because scoring cannot detect the missing name (command semantics
	// dominate the softmax), and skipping the Score call keeps ambient
	// conversation free on weak hardware.
	if ad := cc.Address; ad != nil && !mentionsAnyName(latestUserText(msgs), ad.Names) {
		sendEvent(t, types.ClassifierResultEvent{
			ResponseID: r.id,
			Scores:     []types.ClassifierScore{},
			Threshold:  cc.Threshold,
			Fallback:   types.ClassifierNotAddressed,
		})
		xlog.Debug("realtime classifier: turn does not address the assistant; dropping", "mode", ad.AddressMode())
		if ctx.Err() != nil {
			r.outcome = outcomeCancelled
			return true
		}
		if ad.AddressMode() == types.ClassifierAddressReply && ad.Reply != "" {
			if !emitAssistantMessage(ctx, session, conv, t, r, ad.Reply, overrides) {
				return true
			}
			emitToolCallItems(ctx, session, conv, t, r, nil, true, toolTurn)
			return true
		}
		// ignore: complete the response with no output items.
		emitToolCallItems(ctx, session, conv, t, r, nil, false, toolTurn)
		return true
	}

	// A committed turn can carry no words at all (the VAD fires on noise
	// and the ASR transcribes nothing). Scoring an empty prompt returns a
	// confidently arbitrary winner — measured p≈0.95 for the first option
	// — so skip scoring entirely and treat it like a below-threshold turn.
	var scores []router.LabelScore
	var latency time.Duration
	if strings.TrimSpace(classifierProbe(msgs).Prompt) != "" {
		start := time.Now()
		var err error
		scores, err = session.ModelInterface.ClassifyTurn(ctx, msgs, cc.Options, cc.Normalization)
		if err != nil {
			if cc.FallbackMode() == types.ClassifierFallbackGenerate {
				xlog.Warn("realtime classifier: scoring failed; falling back to generation", "error", err)
				return false
			}
			sendError(t, "classifier_failed", fmt.Sprintf("classifier scoring failed: %v", err), "", "")
			r.outcome = outcomeFailed
			return true
		}
		latency = time.Since(start)
	} else if cc.FallbackMode() == types.ClassifierFallbackGenerate {
		xlog.Debug("realtime classifier: turn has no scorable text; falling back to generation")
		return false
	}

	best := -1
	for i := range scores {
		if best < 0 || scores[i].Score > scores[best].Score {
			best = i
		}
	}
	var chosen *types.ClassifierOption
	chosenID := ""
	fallbackApplied := ""
	if best >= 0 && scores[best].Score >= cc.Threshold {
		chosen = &cc.Options[best]
		chosenID = chosen.ID
	} else {
		fallbackApplied = cc.FallbackMode()
	}

	// Hybrid path: a winning option with argument slots gets them filled by
	// a constrained completion before anything is emitted, so the result
	// event carries the final arguments. An unrecoverable fill failure
	// (error and no complete default set) is handled like a scoring
	// failure.
	filledArgs := ""
	var fillValues map[string]string
	var fillLatency time.Duration
	if chosen != nil {
		var ferr error
		filledArgs, fillValues, fillLatency, ferr = fillChosenArguments(ctx, session, cc, msgs, chosen)
		if ferr != nil {
			if cc.FallbackMode() == types.ClassifierFallbackGenerate {
				xlog.Warn("realtime classifier: slot fill failed; falling back to generation", "error", ferr)
				return false
			}
			sendError(t, "classifier_failed", fmt.Sprintf("classifier slot fill failed: %v", ferr), "", "")
			r.outcome = outcomeFailed
			return true
		}
	}

	evScores := make([]types.ClassifierScore, len(scores))
	for i, s := range scores {
		evScores[i] = types.ClassifierScore{ID: s.Label, Score: s.Score}
	}
	evArgs := ""
	if chosen != nil && chosen.Tool != nil && len(chosen.Tool.Slots) > 0 {
		evArgs = filledArgs
	}
	sendEvent(t, types.ClassifierResultEvent{
		ResponseID:    r.id,
		Scores:        evScores,
		ChosenID:      chosenID,
		Threshold:     cc.Threshold,
		Fallback:      fallbackApplied,
		LatencyMs:     latency.Milliseconds(),
		Arguments:     evArgs,
		FillLatencyMs: fillLatency.Milliseconds(),
	})
	topScore := 0.0
	if best >= 0 {
		topScore = scores[best].Score
	}
	xlog.Debug("realtime classifier: scored turn",
		"chosen", chosenID, "top_score", topScore,
		"threshold", cc.Threshold, "fallback", fallbackApplied,
		"latency_ms", latency.Milliseconds(),
		"arguments", evArgs, "fill_latency_ms", fillLatency.Milliseconds())

	if fallbackApplied == types.ClassifierFallbackGenerate {
		return false
	}

	// Barge-in may have fired during scoring.
	if ctx.Err() != nil {
		r.outcome = outcomeCancelled
		return true
	}

	reply := ""
	var toolCalls []functions.FuncCallResults
	switch {
	case chosen != nil:
		// The reply may template the filled slot values ("Going forward
		// {{distance}} {{units}}.") so what is spoken confirms what was
		// actually inferred.
		reply = chosen.SpliceReply(fillValues)
		if chosen.Tool != nil {
			toolCalls = []functions.FuncCallResults{{Name: chosen.Tool.Name, Arguments: filledArgs}}
		}
	case fallbackApplied == types.ClassifierFallbackReply:
		reply = cc.Fallback.Reply
	default:
		// fallback "none": complete with no output items.
	}

	if reply != "" {
		if !emitAssistantMessage(ctx, session, conv, t, r, reply, overrides) {
			// Cancelled or failed — outcome already recorded.
			return true
		}
	}
	// Always finalize through emitToolCallItems, mirroring the generation
	// path: it emits the function_call items (client executes canned tools
	// and reports back via conversation.item.create) and runs server-side
	// assistant tools inproc.
	emitToolCallItems(ctx, session, conv, t, r, toolCalls, reply != "", toolTurn)
	return true
}

// ---- slot filling (hybrid classify-then-complete) --------------------------
//
// A winning option whose tool declares slots gets its argument values from a
// short constrained completion: the prompt is the exact scoring prompt (warm
// in the backend's cache) continued by the chosen route JSON re-opened at the
// first slot field, and a GBNF grammar pins everything except the slot
// values. The generated tail is parsed back through the JSON object it
// completes, and the values are spliced into the tool's argument template.

// gbnfLiteral renders s as a GBNF quoted literal.
func gbnfLiteral(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

// slotFillGrammar builds the grammar for the completion tail: first slot
// value, then each further slot as a forced `, "<name>": ` literal plus its
// value, then the closing brace.
func slotFillGrammar(slots []types.ClassifierSlot) string {
	var root strings.Builder
	var rules strings.Builder
	needNum, needStr := false, false
	root.WriteString("root ::= ")
	for i := range slots {
		if i > 0 {
			root.WriteString(" " + gbnfLiteral(`, "`+slots[i].Name+`": `) + " ")
		}
		fmt.Fprintf(&root, "slot%d", i)
		fmt.Fprintf(&rules, "\nslot%d ::= ", i)
		switch slots[i].Type {
		case types.ClassifierSlotNumber:
			rules.WriteString("num")
			needNum = true
		case types.ClassifierSlotEnum:
			for vi, v := range slots[i].Values {
				if vi > 0 {
					rules.WriteString(" | ")
				}
				rules.WriteString(gbnfLiteral(`"` + v + `"`))
			}
		default: // string
			rules.WriteString("str")
			needStr = true
		}
	}
	root.WriteString(` "}"`)
	if needNum {
		rules.WriteString("\nnum ::= \"-\"? [0-9] [0-9]* (\".\" [0-9] [0-9]*)?")
	}
	if needStr {
		rules.WriteString("\nstr ::= \"\\\"\" [^\"\\\\\\n]* \"\\\"\"")
	}
	return root.String() + rules.String()
}

// parseSlotValues closes the completed route JSON and extracts each slot's
// value as the string form SpliceArguments expects.
func parseSlotValues(chosenID, firstSlot, generated string, slots []types.ClassifierSlot) (map[string]string, error) {
	idJSON, _ := json.Marshal(chosenID)
	full := `{"route": ` + string(idJSON) + `, "` + firstSlot + `": ` + strings.TrimSpace(generated)
	if !strings.HasSuffix(strings.TrimSpace(generated), "}") {
		full += "}"
	}
	dec := json.NewDecoder(strings.NewReader(full))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("classifier: slot completion %q does not parse: %w", generated, err)
	}
	values := make(map[string]string, len(slots))
	for i := range slots {
		v, ok := obj[slots[i].Name]
		if !ok {
			return nil, fmt.Errorf("classifier: slot completion missing %q", slots[i].Name)
		}
		switch tv := v.(type) {
		case json.Number:
			values[slots[i].Name] = tv.String()
		case string:
			values[slots[i].Name] = tv
		default:
			return nil, fmt.Errorf("classifier: slot %q has unexpected value type %T", slots[i].Name, v)
		}
	}
	return values, nil
}

// fillChosenArguments resolves a winning option's tool arguments: canned
// options pass through, slotted options run the fill completion with a
// default-value recovery when inference fails. The slot values ride along
// so the caller can splice them into the spoken reply too. The error return
// is reserved for unrecoverable failures (no complete default set).
func fillChosenArguments(ctx context.Context, session *Session, cc *types.ClassifierConfig, msgs schema.Messages, chosen *types.ClassifierOption) (args string, values map[string]string, latency time.Duration, err error) {
	if chosen.Tool == nil {
		return "", nil, 0, nil
	}
	if len(chosen.Tool.Slots) == 0 {
		if len(chosen.Tool.Arguments) > 0 {
			return string(chosen.Tool.Arguments), nil, 0, nil
		}
		return "{}", nil, 0, nil
	}
	start := time.Now()
	args, values, err = session.ModelInterface.FillToolArguments(ctx, msgs, cc.Options, cc.Normalization, chosen)
	latency = time.Since(start)
	if err == nil {
		return args, values, latency, nil
	}
	xlog.Warn("realtime classifier: slot fill failed; trying slot defaults", "option", chosen.ID, "error", err)
	defaults, derr := chosen.Tool.SlotDefaults()
	if derr != nil {
		return "", nil, latency, err
	}
	args, derr = chosen.Tool.SpliceArguments(defaults)
	if derr != nil {
		return "", nil, latency, err
	}
	return args, defaults, latency, nil
}

// classifierPolicyDescription renders an option's scoring description,
// appending any slot declarations so the model both weighs the parameters
// during scoring and knows how to fill them ("assume meters…") during the
// slot completion — the hints ride the shared system prompt, costing no
// extra per-turn tokens.
func classifierPolicyDescription(o *types.ClassifierOption) string {
	if o.Tool == nil || len(o.Tool.Slots) == 0 {
		return o.Description
	}
	var b strings.Builder
	b.WriteString(o.Description)
	b.WriteString(" — route parameters:")
	for i := range o.Tool.Slots {
		s := &o.Tool.Slots[i]
		if i > 0 {
			b.WriteString(";")
		}
		b.WriteString(" " + s.Name)
		switch s.Type {
		case types.ClassifierSlotEnum:
			b.WriteString(" (one of: " + strings.Join(s.Values, ", ") + ")")
		default:
			b.WriteString(" (" + s.Type + ")")
		}
		if s.Hint != "" {
			b.WriteString(", " + s.Hint)
		}
	}
	return b.String()
}
