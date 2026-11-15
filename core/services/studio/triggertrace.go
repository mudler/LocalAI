package studio

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// triggerToolNames are the model tools of the triggers group, in the order the
// trace lists them.
var triggerToolNames = []string{proposeTriggerName, listTriggersName, updateTriggerName, deleteTriggerName}

// maxTraceText cuts the error text of a tool call in the log.
const maxTraceText = 200

// triggerToolsAttrs is the structured part of the per-turn trace line: which
// trigger tools the turn was given, which were refused and why, and whether the
// sender held the capability. It holds no message content. The refusal reasons
// are the ones Build gave, in the words of the code.
func triggerToolsAttrs(rec session.ChatRecord, c builtins.Chat, built []builtins.Built) []any {
	var offered, refused []string
	for _, id := range triggerToolNames {
		for _, b := range built {
			if b.ID != id {
				continue
			}
			if b.OK {
				offered = append(offered, id)
			} else {
				refused = append(refused, id+"="+b.Reason)
			}
		}
	}
	attrs := []any{"chat", rec.ID, "origin", originText(rec.Origin), "origin_id", rec.OriginID, "agent", rec.AgentID,
		"sub_agent", c.SubAgent, "offered", strings.Join(offered, ","), "refused", strings.Join(refused, "; ")}
	if c.Allow != nil {
		held, _ := c.Allow(CapTriggersPropose)
		attrs = append(attrs, "sender_holds_propose", held)
		held, _ = c.Allow(CapTriggersChange)
		attrs = append(attrs, "sender_holds_change", held)
	}
	return attrs
}

func originText(o string) string {
	if o == "" {
		return "studio"
	}
	return o
}

// traceTriggerTools writes one info line per turn with the trigger tools the
// turn was given and the reason for each one it was not. Nothing records the
// tool list of a turn anywhere else, so this line is how a missing reminder
// tool is traced back afterwards.
func (s *Service) traceTriggerTools(rec session.ChatRecord, c builtins.Chat, built []builtins.Built) {
	attrs := triggerToolsAttrs(rec, c, built)
	if mode := s.approvalMode(context.Background(), rec); mode != "" {
		attrs = append(attrs, "approval_mode", mode)
	}
	s.log.Info("studio: trigger tools of the turn", attrs...)
}

// approvalMode is the approval mode of the connection that started the chat
// (ask, auto_readonly or auto), "" for a chat that no connection started.
func (s *Service) approvalMode(ctx context.Context, rec session.ChatRecord) string {
	if rec.Origin != session.OriginConnector || s.connectors == nil {
		return ""
	}
	return s.connectors.ApprovalOf(ctx, rec.OriginID).ModeOrDefault()
}

// tracedTriggerRun logs the outcome of each call of a trigger tool: the tool,
// whether it worked, the error text and the ref it touched. The arguments and
// the result text are not logged: they hold what the person wrote.
func (s *Service) tracedTriggerRun(name string, c builtins.Chat, run func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		out, err := run(ctx, raw)
		attrs := []any{"tool", name, "chat", c.Actor.ChatID, "origin", originText(c.Origin), "agent", c.AgentID, "ok", err == nil}
		if mode := s.approvalMode(ctx, session.ChatRecord{Origin: c.Origin, OriginID: c.OriginID}); mode != "" {
			attrs = append(attrs, "approval_mode", mode)
		}
		if ref := refOfCall(raw, out); ref != "" {
			attrs = append(attrs, "ref", ref)
		}
		if err != nil {
			msg := err.Error()
			if len(msg) > maxTraceText {
				msg = strings.ToValidUTF8(msg[:maxTraceText], "") + "..."
			}
			attrs = append(attrs, "error", msg)
		}
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		s.log.Log(ctx, level, "studio: trigger tool call", attrs...)
		return out, err
	}
}

// refOfCall is the ref a call touched: the one it was given, or the one the
// stored state in its result names.
func refOfCall(raw json.RawMessage, out string) string {
	var in struct {
		Ref string `json:"ref"`
	}
	if json.Unmarshal(raw, &in) == nil && in.Ref != "" {
		return in.Ref
	}
	_, after, found := strings.Cut(out, proofMark)
	if !found {
		return ""
	}
	var proof struct {
		Ref string `json:"ref"`
	}
	if err := json.NewDecoder(strings.NewReader(after)).Decode(&proof); err != nil {
		return ""
	}
	return proof.Ref
}
