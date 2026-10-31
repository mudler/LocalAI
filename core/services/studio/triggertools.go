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
	updateTriggerName  = "update_trigger"
	deleteTriggerName  = "delete_trigger"
	listTriggersMax    = 50
)

const proposeTriggerDescription = "Propose a trigger: a task that runs by itself, at a time or when a file arrives in a folder. " +
	"Propose only when the user asks for something that repeats or that must happen at a given time or when a file arrives. " +
	"A proposal does nothing by itself: the user must confirm it in the chat or on the Triggers page, and until then it is not active, so never say that the trigger is active or will run. " +
	"Give the name and the prompt that the run will get. The prompt must stand alone (the run knows nothing else): what to read, what to write. " +
	"It must not ask the run to print JSON or any other output: LocalAI already tells the run to finish with one short plain sentence, which the user reads. " +
	"when is a schedule text (\"daily 08:00\", \"weekdays 09:30\", \"weekly mon,thu 09:30\", \"monthly 15 08:00\", \"once 2026-12-24 18:00\" or five cron fields) " +
	"or {\"file_arrives_in\": folder} with a folder inside the user's files, such as /Contracts. " +
	"A schedule runs at most once an hour; a shorter one is refused. " +
	"time_zone is an IANA name such as Europe/Rome; when absent, the person's own zone is used when they confirm. " +
	"Fill reads, writes, widget and estimated_tokens so the person sees what a run does and costs before they confirm: " +
	"writes is the data file a run rewrites and widget the id of the Home widget in home.json that shows it."

const proposeTriggerSchema = `{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":80,"description":"A short name for the trigger."},` +
	`"prompt":{"type":"string","minLength":1,"description":"What the agent must do at each run."},` +
	`"when":{"description":"A schedule text, or an object with the folder to watch.","oneOf":[` +
	`{"type":"string"},` +
	`{"type":"object","properties":{"file_arrives_in":{"type":"string","description":"A folder inside the user's files."}},"required":["file_arrives_in"]}]},` +
	`"time_zone":{"type":"string","description":"An IANA time zone name such as Europe/Rome; when absent, the person's own zone is used when they confirm."},` +
	`"daily_cap":{"type":"integer","minimum":0,"maximum":1000,"description":"The most runs in one day; 0 is the default limit."},` +
	`"reads":{"type":"string","maxLength":200,"description":"What a run reads, in a few words: Mail from the last 24 hours."},` +
	`"writes":{"type":"string","maxLength":256,"description":"The file in the user's home that a run rewrites, such as data/inbox.json."},` +
	`"widget":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,39}$","description":"The id of the Home widget in home.json that shows the file."},` +
	`"estimated_tokens":{"type":"integer","minimum":0,"maximum":10000000,"description":"About how many tokens one run uses, prompt and answer."}},` +
	`"required":["name","prompt","when"]}`

const listTriggersDescription = "List the triggers of the user: a ref, the name, when it runs or which folder it watches, whether it is on or off, and how its last run ended. " +
	"It lists at most 50 and shows no prompts. Use the ref to name a trigger in update_trigger and delete_trigger. " +
	"To read the prompt and the other settings of ONE trigger before you change it, pass its ref and include_prompt: true."

const listTriggersSchema = `{"type":"object","properties":{` +
	`"ref":{"type":"string","description":"The ref of one trigger, from an earlier list_triggers; only that trigger is listed."},` +
	`"include_prompt":{"type":"boolean","description":"With ref: also return the prompt, the schedule text and the plan of that one trigger."}}}`

const updateTriggerDescription = "Change a trigger of the user, named by its ref from list_triggers. Give only the fields that change. " +
	"enabled false pauses the trigger and enabled true resumes it. " +
	"A change that only pauses or resumes applies at once, and the user can undo it on the card in the chat: then say that it is paused or resumed. " +
	"Any other change does nothing by itself: a card shows the user the fields before and after, and nothing changes until they press Apply, so never say that the change is made until they do. " +
	"The same rules as propose_trigger apply: the prompt must stand alone and must not ask the run to print JSON; when is a schedule text or {\"file_arrives_in\": folder}; a schedule runs at most once an hour, and writes is a path inside the user's files."

const updateTriggerSchema = `{"type":"object","properties":{` +
	`"ref":{"type":"string","minLength":1,"description":"The ref of the trigger, from list_triggers."},` +
	`"name":{"type":"string","minLength":1,"maxLength":80},` +
	`"prompt":{"type":"string","minLength":1,"description":"What the agent must do at each run."},` +
	`"when":{"description":"A schedule text, or an object with the folder to watch.","oneOf":[` +
	`{"type":"string"},` +
	`{"type":"object","properties":{"file_arrives_in":{"type":"string","description":"A folder inside the user's files."}},"required":["file_arrives_in"]}]},` +
	`"time_zone":{"type":"string","description":"An IANA time zone name such as Europe/Rome."},` +
	`"daily_cap":{"type":"integer","minimum":0,"maximum":1000,"description":"The most runs in one day; 0 is the default limit."},` +
	`"enabled":{"type":"boolean","description":"false pauses the trigger, true resumes it."},` +
	`"reads":{"type":"string","maxLength":200},` +
	`"writes":{"type":"string","maxLength":256,"description":"The file in the user's home that a run rewrites."},` +
	`"widget":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,39}$"},` +
	`"estimated_tokens":{"type":"integer","minimum":0,"maximum":10000000}},` +
	`"required":["ref"]}`

const deleteTriggerDescription = "Ask to delete a trigger of the user, named by its ref from list_triggers. " +
	"It does nothing by itself: a card shows the user the trigger, and it is deleted only when they press Delete; they can undo a delete for a day. " +
	"Never say that the trigger is deleted until they do."

const deleteTriggerSchema = `{"type":"object","properties":{"ref":{"type":"string","minLength":1,"description":"The ref of the trigger, from list_triggers."}},"required":["ref"]}`

// triggerTools returns the trigger tools of a chat started by a person. A
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
		{Name: listTriggersName, Description: listTriggersDescription, Schema: json.RawMessage(listTriggersSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.listTool(ctx, a, raw) }},
		{Name: updateTriggerName, Description: updateTriggerDescription, Schema: json.RawMessage(updateTriggerSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.updateTool(ctx, a, raw) }},
		{Name: deleteTriggerName, Description: deleteTriggerDescription, Schema: json.RawMessage(deleteTriggerSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.deleteTool(ctx, a, raw) }},
	}
}

func (h *Triggers) proposeTool(ctx context.Context, a workspace.Actor, rec session.ChatRecord, raw json.RawMessage) (string, error) {
	var args struct {
		Name     string          `json:"name"`
		Prompt   string          `json:"prompt"`
		When     json.RawMessage `json:"when"`
		TimeZone string          `json:"time_zone"`
		DailyCap int             `json:"daily_cap"`
		Reads    string          `json:"reads"`
		Writes   string          `json:"writes"`
		Widget   string          `json:"widget"`
		Tokens   int             `json:"estimated_tokens"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	in := TriggerInput{Name: args.Name, Prompt: args.Prompt, DailyCap: args.DailyCap, TZ: args.TimeZone,
		SourceChatID: a.ChatID, AgentID: rec.AgentID, Model: rec.Model,
		Reads: args.Reads, Writes: args.Writes, Widget: args.Widget, EstTokens: args.Tokens}
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

func (h *Triggers) listTool(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		Ref           string `json:"ref"`
		IncludePrompt bool   `json:"include_prompt"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	type detail struct {
		Prompt    string `json:"prompt"`
		Schedule  string `json:"schedule,omitempty"`
		TimeZone  string `json:"time_zone,omitempty"`
		Folder    string `json:"folder,omitempty"`
		DailyCap  int    `json:"daily_cap"`
		Reads     string `json:"reads,omitempty"`
		Writes    string `json:"writes,omitempty"`
		Widget    string `json:"widget,omitempty"`
		EstTokens int    `json:"estimated_tokens,omitempty"`
	}
	type row struct {
		Ref     string  `json:"ref"`
		Name    string  `json:"name"`
		When    string  `json:"when"`
		On      bool    `json:"on"`
		LastRun string  `json:"last_run,omitempty"`
		Detail  *detail `json:"detail,omitempty"`
	}
	var all []triggers.Trigger
	switch {
	case args.Ref != "":
		t, err := h.ResolveTrigger(ctx, a.Principal, args.Ref)
		if err != nil {
			return "", errors.New(strings.TrimPrefix(err.Error(), "studio: "))
		}
		all = []triggers.Trigger{t}
	case args.IncludePrompt:
		return "", errors.New("include_prompt needs the ref of one trigger")
	default:
		var err error
		if all, err = h.ListTriggers(ctx, a.Principal); err != nil {
			return "", err
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	if len(all) > listTriggersMax {
		all = all[:listTriggersMax]
	}
	rows := make([]row, 0, len(all))
	for _, t := range all {
		r := row{Ref: TriggerRef(t.ID), Name: t.Name, When: h.summary(t), On: t.Enabled}
		if runs, err := h.o.Store.ListRuns(ctx, t.ID, 1); err == nil && len(runs) == 1 {
			r.LastRun = string(runs[0].Status)
			if runs[0].Reason != "" {
				r.LastRun += ": " + runs[0].Reason
			}
		}
		if args.IncludePrompt {
			r.Detail = &detail{Prompt: t.Prompt, Schedule: t.Schedule, TimeZone: t.TZ, Folder: h.RelFolder(t), DailyCap: t.DailyCap,
				Reads: t.Reads, Writes: t.Writes, Widget: t.Widget, EstTokens: t.EstTokens}
		}
		rows = append(rows, r)
	}
	out, _ := json.Marshal(map[string]any{"triggers": rows})
	return string(out), nil
}

// toolErr is the text of a service error for the model, without the package
// prefix.
func toolErr(err error) error {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, ErrBadTrigger.Error()+": ")
	msg = strings.TrimPrefix(msg, "studio: ")
	return errors.New(msg)
}

func (h *Triggers) updateTool(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		Ref      string          `json:"ref"`
		Name     *string         `json:"name"`
		Prompt   *string         `json:"prompt"`
		When     json.RawMessage `json:"when"`
		TimeZone *string         `json:"time_zone"`
		DailyCap *int            `json:"daily_cap"`
		Enabled  *bool           `json:"enabled"`
		Reads    *string         `json:"reads"`
		Writes   *string         `json:"writes"`
		Widget   *string         `json:"widget"`
		Tokens   *int            `json:"estimated_tokens"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	target, err := h.ResolveTrigger(ctx, a.Principal, args.Ref)
	if err != nil {
		return "", toolErr(err)
	}
	p := TriggerPatch{Name: args.Name, Prompt: args.Prompt, TZ: args.TimeZone, DailyCap: args.DailyCap, Enabled: args.Enabled,
		Reads: args.Reads, Writes: args.Writes, Widget: args.Widget, EstTokens: args.Tokens}
	if len(args.When) > 0 && string(args.When) != "null" {
		var text string
		var obj struct {
			FileArrivesIn string `json:"file_arrives_in"`
		}
		switch {
		case json.Unmarshal(args.When, &text) == nil && text != "":
			p.Kind, p.Schedule = triggers.KindSchedule, text
		case json.Unmarshal(args.When, &obj) == nil && obj.FileArrivesIn != "":
			p.Kind, p.Folder = triggers.KindFile, obj.FileArrivesIn
		default:
			return "", errors.New("when must be a schedule text or {\"file_arrives_in\": folder}")
		}
	}
	rec, applied, err := h.ProposeChange(ctx, a.Principal, a.ChatID, target, p)
	if err != nil {
		return "", toolErr(err)
	}
	h.changeCard(ctx, a, rec)
	if applied {
		state := "paused: it does not run until it is resumed"
		if rec.Change.Op == triggers.ChangeResume {
			state = "resumed: it runs again"
		}
		return fmt.Sprintf("%q is %s. The user can undo this on the card in the chat.", target.Name, state), nil
	}
	return fmt.Sprintf("Asked to change %q (%s). Nothing is changed yet: the user must press Apply on the card in the chat. Do not say it changed until they do.",
		target.Name, strings.Join(changedFields(target, asTrigger(rec)), ", ")), nil
}

func (h *Triggers) deleteTool(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	target, err := h.ResolveTrigger(ctx, a.Principal, args.Ref)
	if err != nil {
		return "", toolErr(err)
	}
	rec, err := h.ProposeDelete(ctx, a.Principal, a.ChatID, target)
	if err != nil {
		return "", toolErr(err)
	}
	h.changeCard(ctx, a, rec)
	return fmt.Sprintf("Asked to delete %q (%s). It is not deleted: the user must press Delete on the card in the chat. Do not say it is deleted until they do.",
		target.Name, h.summary(target)), nil
}

// changeCard puts the card of a change record on the tool line, so the
// interface draws it below the message, also after a reload; the event tells a
// client that is watching.
func (h *Triggers) changeCard(ctx context.Context, a workspace.Actor, rec triggers.Trigger) {
	session.SetToolCard(ctx, session.ToolCard{Kind: ChangeCardKind, ID: rec.ID})
	h.svc.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindTriggerProposed, IDs: []string{rec.ID}, Text: rec.Name})
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
