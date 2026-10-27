package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ProposalCardKind is the Kind of the tool card that points at a trigger
// proposal; the card's ID is the proposal's ID.
const ProposalCardKind = "trigger_proposal"

const (
	proposeTriggerName = "propose_trigger"
	listTriggersName   = "list_triggers"
	listTriggersMax    = 50
)

const proposeTriggerDescription = "Propose a trigger: a task that runs by itself, at a time or when a file arrives in a folder. " +
	"Propose only when the user asks for something that repeats or that must happen at a given time or when a file arrives. " +
	"A proposal does nothing by itself: the user must confirm it in the chat or on the Triggers page, and until then it is not active, so never say that the trigger is active or will run. " +
	"Give the name and the prompt that the run will get. " +
	"when is a schedule text (\"daily 08:00\", \"weekdays 09:30\", \"weekly mon,thu 09:30\", \"monthly 15 08:00\", \"once 2026-12-24 18:00\" or five cron fields) " +
	"or {\"file_arrives_in\": folder} with a folder inside the user's files, such as /Contracts. " +
	"time_zone is an IANA name such as Europe/Rome; when absent, the person's own zone is used when they confirm."

const proposeTriggerSchema = `{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":80,"description":"A short name for the trigger."},` +
	`"prompt":{"type":"string","minLength":1,"description":"What the agent must do at each run."},` +
	`"when":{"description":"A schedule text, or an object with the folder to watch.","oneOf":[` +
	`{"type":"string"},` +
	`{"type":"object","properties":{"file_arrives_in":{"type":"string","description":"A folder inside the user's files."}},"required":["file_arrives_in"]}]},` +
	`"time_zone":{"type":"string","description":"An IANA time zone name such as Europe/Rome; when absent, the person's own zone is used when they confirm."},` +
	`"daily_cap":{"type":"integer","minimum":0,"maximum":1000,"description":"The most runs in one day; 0 is the default limit."}},` +
	`"required":["name","prompt","when"]}`

const listTriggersDescription = "List the triggers of the user: the name, when it runs or which folder it watches, whether it is on or off, and how its last run ended. " +
	"It lists at most 50 and shows neither prompts nor IDs."

// triggerTools returns the two trigger tools of a chat started by a person. A
// run that a trigger or a connector started never gets them, and neither does a
// sub-agent: they are not part of hostTools.
func (s *Service) triggerTools(a workspace.Actor, rec session.ChatRecord) []session.Tool {
	h := s.triggers
	if h == nil || rec.Origin != "" || rec.Kind == session.KindSubAgent {
		return nil
	}
	return []session.Tool{
		{Name: proposeTriggerName, Description: proposeTriggerDescription, Schema: json.RawMessage(proposeTriggerSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.proposeTool(ctx, a, rec, raw) }},
		{Name: listTriggersName, Description: listTriggersDescription, Schema: json.RawMessage(`{"type":"object","properties":{}}`),
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) { return h.listTool(ctx, a) }},
	}
}

func (h *Triggers) proposeTool(ctx context.Context, a workspace.Actor, rec session.ChatRecord, raw json.RawMessage) (string, error) {
	var args struct {
		Name     string          `json:"name"`
		Prompt   string          `json:"prompt"`
		When     json.RawMessage `json:"when"`
		TimeZone string          `json:"time_zone"`
		DailyCap int             `json:"daily_cap"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	in := TriggerInput{Name: args.Name, Prompt: args.Prompt, DailyCap: args.DailyCap, TZ: args.TimeZone,
		SourceChatID: a.ChatID, AgentID: rec.AgentID, Model: rec.Model}
	var text string
	var obj struct {
		FileArrivesIn string `json:"file_arrives_in"`
	}
	switch {
	case json.Unmarshal(args.When, &text) == nil && text != "":
		in.Kind, in.Schedule = triggers.KindSchedule, text
	case json.Unmarshal(args.When, &obj) == nil && obj.FileArrivesIn != "":
		in.Kind, in.Folder, in.TZ = triggers.KindFile, obj.FileArrivesIn, ""
	default:
		return "", errors.New("when must be a schedule text or {\"file_arrives_in\": folder}")
	}
	t, err := h.ProposeTrigger(ctx, a.Principal, in)
	if err != nil {
		return "", errors.New(strings.TrimPrefix(err.Error(), ErrBadTrigger.Error()+": "))
	}
	// The card lets the interface show Create, Edit and Dismiss on this tool
	// line, also after a reload; the event tells a client that is watching.
	session.SetToolCard(ctx, session.ToolCard{Kind: ProposalCardKind, ID: t.ID})
	h.svc.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindTriggerProposed, IDs: []string{t.ID}, Text: t.Name})
	return fmt.Sprintf("Proposed %q (%s). It is not active: the user must confirm it in the chat or on the Triggers page. Do not say it will run until they do.", t.Name, h.summary(t)), nil
}

func (h *Triggers) listTool(ctx context.Context, a workspace.Actor) (string, error) {
	all, err := h.ListTriggers(ctx, a.Principal)
	if err != nil {
		return "", err
	}
	type row struct {
		Name    string `json:"name"`
		When    string `json:"when"`
		On      bool   `json:"on"`
		LastRun string `json:"last_run,omitempty"`
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	if len(all) > listTriggersMax {
		all = all[:listTriggersMax]
	}
	rows := make([]row, 0, len(all))
	for _, t := range all {
		r := row{Name: t.Name, When: h.summary(t), On: t.Enabled}
		if runs, err := h.o.Store.ListRuns(ctx, t.ID, 1); err == nil && len(runs) == 1 {
			r.LastRun = string(runs[0].Status)
			if runs[0].Reason != "" {
				r.LastRun += ": " + runs[0].Reason
			}
		}
		rows = append(rows, r)
	}
	out, _ := json.Marshal(map[string]any{"triggers": rows})
	return string(out), nil
}

// Summary is a plain-English line for a trigger: "Every weekday at 09:00", or
// "New files in /Contracts".
func (h *Triggers) Summary(t triggers.Trigger) string { return h.summary(t) }

func (h *Triggers) summary(t triggers.Trigger) string {
	if t.Kind == triggers.KindFile {
		return "new files in " + h.RelFolder(t)
	}
	sch, err := triggers.ParseSchedule(t.Schedule)
	if err != nil {
		return t.Schedule
	}
	s := sch.String()
	if t.TZ != "" {
		s += " (" + t.TZ + ")"
	}
	return s
}
