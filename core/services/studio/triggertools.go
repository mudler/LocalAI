package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
	"writes is the data file a run rewrites and widget the id of the Home widget in home.json that shows it. " +
	"A run with nobody present only reads unless acts lists what it may also do; ask for the least it needs, and only for what the user's request needs. " +
	"The proposal shows the acts to the user before they confirm. " + triggerFilesNote

// triggerFilesNote ends the descriptions of propose_trigger and update_trigger.
// It keeps a model from putting a script or a state file of a trigger in a
// place that a run cannot see.
const triggerFilesNote = "Files: " + workfiles.TriggerFileRule + " Name such a path in the prompt as it is under /workspace/home."

// chatPathWarning is the warning for the model when the prompt or the plan of a
// trigger names a path under /workspace/chat. A run starts a new chat, so the
// path is empty there. It returns "" when no text names such a path.
func chatPathWarning(texts ...string) string {
	for _, t := range texts {
		if strings.Contains(t, "/workspace/chat") {
			return " Warning: the prompt or the plan names a path under /workspace/chat. Every run of a trigger starts a new chat with an empty /workspace/chat, " +
				"so a file there is not found. Move the script and its state under /workspace/home and change the trigger to name those paths."
		}
	}
	return ""
}

const proposeTriggerSchema = `{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":80,"description":"A short name for the trigger."},` +
	`"prompt":{"type":"string","minLength":1,"description":"What the agent must do at each run."},` +
	`"when":{"description":"A schedule text, or an object with the folder to watch.","oneOf":[` +
	`{"type":"string"},` +
	`{"type":"object","properties":{"file_arrives_in":{"type":"string","description":"A folder inside the user's files."}},"required":["file_arrives_in"]}]},` +
	`"time_zone":{"type":"string","description":"An IANA time zone name such as Europe/Rome; when absent, the person's own zone is used when they confirm."},` +
	`"daily_cap":{"type":"integer","minimum":0,"maximum":1000,"description":"The most runs in one day; 0 is the default limit."},` +
	`"run_limit":{"type":"string","description":"The longest one run may take, as a duration such as 30m or 2h, from 1m to 24h. Leave it out for the default. The administrator sets a ceiling: a longer limit is cut to it."},` +
	`"reads":{"type":"string","maxLength":200,"description":"What a run reads, in a few words: Mail from the last 24 hours."},` +
	`"writes":{"type":"string","maxLength":256,"description":"The file in the user's home that a run rewrites, such as data/inbox.json."},` +
	`"widget":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,39}$","description":"The id of the Home widget in home.json that shows the file."},` +
	`"acts":{"type":"array","maxItems":8,"items":{"type":"string","enum":["act.files","act.addons","act.send","act.spend"]},"description":"What a run may do when nobody is present: act.files writes files in the agent's folders, act.addons uses the actions of its add-ons, act.send sends messages beyond the delivery target, act.spend uses paid or external tools and the network. Leave it out for a run that only reads and reports. Only what your agent holds; the person sees the list on the proposal before they confirm, and nothing can widen it afterwards."},` +
	`"estimated_tokens":{"type":"integer","minimum":0,"maximum":10000000,"description":"About how many tokens one run uses, prompt and answer."}},` +
	`"required":["name","prompt","when"]}`

// proposeTriggerConnectorSchema is the schema of propose_trigger in a chat of
// a messaging app: the same, with the delivery of the runs.
const proposeTriggerConnectorSchema = `{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":80,"description":"A short name for the trigger."},` +
	`"prompt":{"type":"string","minLength":1,"description":"What the agent must do at each run."},` +
	`"when":{"description":"A schedule text, an object with the folder to watch, or a one-off timer: {\"in\": \"10 minutes\"} or {\"at\": \"18:00\"}.","oneOf":[` +
	`{"type":"string"},` +
	`{"type":"object","properties":{"file_arrives_in":{"type":"string","description":"A folder inside the user's files."}},"required":["file_arrives_in"]},` +
	`{"type":"object","properties":{"in":{"type":"string","description":"A delay from now, at least one minute: 10 minutes, 1h30m, 2 days."}},"required":["in"]},` +
	`{"type":"object","properties":{"at":{"type":"string","description":"A time of day, the next time it is that time: 18:00 or 6pm. Needs time_zone."}},"required":["at"]}]},` +
	`"time_zone":{"type":"string","description":"An IANA time zone name such as Europe/Rome; when absent, UTC is used. Needed for a time of day."},` +
	`"access":{"type":"string","enum":["read_only","read_write"],"description":"read_only only when the runs just read and report: they write no file, run no command and message nobody else. Otherwise read_write (the default)."},` +
	`"daily_cap":{"type":"integer","minimum":0,"maximum":1000,"description":"The most runs in one day; 0 is the default limit."},` +
	`"run_limit":{"type":"string","description":"The longest one run may take, as a duration such as 30m or 2h, from 1m to 24h. Leave it out for the default. The administrator sets a ceiling: a longer limit is cut to it."},` +
	`"reads":{"type":"string","maxLength":200,"description":"What a run reads, in a few words."},` +
	`"writes":{"type":"string","maxLength":256,"description":"The file in the user's home that a run rewrites."},` +
	`"widget":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,39}$","description":"The id of the Home widget that shows the file."},` +
	`"acts":{"type":"array","maxItems":8,"items":{"type":"string","enum":["act.files","act.addons","act.send","act.spend"]},"description":"What a run may do when nobody is present: act.files writes files in the agent's folders, act.addons uses the actions of its add-ons, act.send sends messages beyond the delivery target, act.spend uses paid or external tools and the network. Leave it out for a run that only reads and reports. Only what your agent holds; the person sees the list on the proposal before they confirm, and nothing can widen it afterwards."},` +
	`"estimated_tokens":{"type":"integer","minimum":0,"maximum":10000000,"description":"About how many tokens one run uses."},` +
	`"delivery":{"type":"object","description":"Where the answer of each run goes. Default: the owner's Inbox.","properties":{` +
	`"target":{"type":"string","enum":["inbox","origin","channel"],"description":"origin writes the answer in this chat and thread, inbox in the owner's Inbox, channel in the channel itself, at the top and not in a thread, for a channel the owner approved (name it in channel)."},` +
	`"channel":{"type":"string","description":"With target channel: the name or the ID of one of the approved channels."},` +
	`"mode":{"type":"string","enum":["always","on_change","on_problem"],"description":"always: every run. on_change: only when the answer differs from the run before. on_problem: only when a run fails."}}}},` +
	`"required":["name","prompt","when"]}`

// connectorProposeNote is added to the description of propose_trigger in a
// chat of a messaging app.
const connectorProposeNote = " This chat is in a messaging app. The host writes the proposal in the chat itself, " +
	"and the person answers there in plain words, so do not repeat the proposal or any code. Depending on the owner's settings the host may also set the trigger up at once and tell the person; " +
	"the result of this tool says which happened, so never say more than it says. " +
	"For \"do it in 10 minutes\" or \"at 18:00\" use a one-off timer: when {\"in\": \"10 minutes\"} or {\"at\": \"18:00\"} with time_zone; it runs once, at least a minute from now, and is then removed. " +
	"Every run is one model run, so propose a recurring trigger only when the person asks for one; a recurring one runs at most once an hour. " +
	"Use delivery.target origin when the person wants the result in this chat, and a mode that fits (on_change for a watch, on_problem for a check). " +
	"Set access read_only only when the runs just read and report. " +
	"A run only reads unless acts says more, and only the acts named below can be asked for here; anything else is dropped and the person is told. " +
	"A proposal lapses after 24 hours, and at most 3 can be made in an hour."

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
	"This holds for acts and delivery too: a change that widens acts, or moves the answers to another place, is applied only when the user presses Apply on the card. " +
	"acts is the whole list that the trigger should have, within what the agent holds; delivery is only for a trigger that was proposed in a messaging app. " +
	"The same rules as propose_trigger apply: the prompt must stand alone and must not ask the run to print JSON; when is a schedule text or {\"file_arrives_in\": folder}; a schedule runs at most once an hour, and writes is a path inside the user's files. " + triggerFilesNote

const updateTriggerSchema = `{"type":"object","properties":{` +
	`"ref":{"type":"string","minLength":1,"description":"The ref of the trigger, from list_triggers."},` +
	`"name":{"type":"string","minLength":1,"maxLength":80},` +
	`"prompt":{"type":"string","minLength":1,"description":"What the agent must do at each run."},` +
	`"when":{"description":"A schedule text, or an object with the folder to watch.","oneOf":[` +
	`{"type":"string"},` +
	`{"type":"object","properties":{"file_arrives_in":{"type":"string","description":"A folder inside the user's files."}},"required":["file_arrives_in"]}]},` +
	`"time_zone":{"type":"string","description":"An IANA time zone name such as Europe/Rome."},` +
	`"daily_cap":{"type":"integer","minimum":0,"maximum":1000,"description":"The most runs in one day; 0 is the default limit."},` +
	`"run_limit":{"type":"string","description":"The longest one run may take, as a duration such as 30m or 2h, from 1m to 24h. Leave it out for the default. The administrator sets a ceiling: a longer limit is cut to it."},` +
	`"enabled":{"type":"boolean","description":"false pauses the trigger, true resumes it."},` +
	`"reads":{"type":"string","maxLength":200},` +
	`"writes":{"type":"string","maxLength":256,"description":"The file in the user's home that a run rewrites."},` +
	`"widget":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,39}$"},` +
	`"acts":{"type":"array","maxItems":8,"items":{"type":"string","enum":["act.files","act.addons","act.send","act.spend"]},"description":"The full list of what a run may do when nobody is present; it replaces the list the trigger has, and [] takes every act away. Only what the agent holds, and for a trigger from a messaging app only what the owner allows for that connection. The change waits for the person on the card."},` +
	`"delivery":{"type":"object","description":"Where the answer of each run goes. Only for a trigger that was proposed in a messaging app.","properties":{` +
	`"target":{"type":"string","enum":["inbox","origin","channel"],"description":"origin writes the answer in the chat and thread where the trigger was proposed, inbox in the owner's Inbox, channel in an approved channel itself, at the top and not in a thread."},` +
	`"channel":{"type":"string","description":"With target channel: the name or the ID of one of the approved channels."},` +
	`"mode":{"type":"string","enum":["always","on_change","on_problem"],"description":"always: every run. on_change: only when the answer differs from the run before. on_problem: only when a run fails."}}},` +
	`"estimated_tokens":{"type":"integer","minimum":0,"maximum":10000000}},` +
	`"required":["ref"]}`

const deleteTriggerDescription = "Ask to delete a trigger of the user, named by its ref from list_triggers. " +
	"It does nothing by itself: a card shows the user the trigger, and it is deleted only when they press Delete; they can undo a delete for a day. " +
	"Never say that the trigger is deleted until they do."

const deleteTriggerSchema = `{"type":"object","properties":{"ref":{"type":"string","minLength":1,"description":"The ref of the trigger, from list_triggers."}},"required":["ref"]}`

// registerTriggerTools adds the four trigger tools to the registry of built-in
// tools, as one switch group: a chat started by a person has them until it
// switches the group off. A run that a trigger or a connector started never
// gets them, and neither does a sub-agent. The operator's start-up switch
// (LOCALAI_DISABLE_TRIGGERS) governs them, so they have no runtime setting and
// Settings > Tools does not list them.
func (s *Service) registerTriggerTools() error {
	type def struct {
		id, label, desc, schema string
		readOnly                bool
		run                     func(h *Triggers, a workspace.Actor, c builtins.Chat) func(context.Context, json.RawMessage) (string, error)
	}
	defs := []def{
		{proposeTriggerName, "Propose a trigger", proposeTriggerDescription, proposeTriggerSchema, false,
			func(h *Triggers, a workspace.Actor, c builtins.Chat) func(context.Context, json.RawMessage) (string, error) {
				return func(ctx context.Context, raw json.RawMessage) (string, error) {
					return h.proposeTool(ctx, a, c, raw)
				}
			}},
		{listTriggersName, "List triggers", listTriggersDescription, listTriggersSchema, true,
			func(h *Triggers, a workspace.Actor, c builtins.Chat) func(context.Context, json.RawMessage) (string, error) {
				return func(ctx context.Context, raw json.RawMessage) (string, error) {
					return h.listTool(ctx, a, c.AgentID, c.Origin == session.OriginConnector, raw)
				}
			}},
		{updateTriggerName, "Change a trigger", updateTriggerDescription, updateTriggerSchema, false,
			func(h *Triggers, a workspace.Actor, c builtins.Chat) func(context.Context, json.RawMessage) (string, error) {
				return func(ctx context.Context, raw json.RawMessage) (string, error) {
					return h.updateTool(ctx, a, c, raw)
				}
			}},
		{deleteTriggerName, "Delete a trigger", deleteTriggerDescription, deleteTriggerSchema, false,
			func(h *Triggers, a workspace.Actor, c builtins.Chat) func(context.Context, json.RawMessage) (string, error) {
				return func(ctx context.Context, raw json.RawMessage) (string, error) {
					return h.deleteTool(ctx, a, c.AgentID, raw)
				}
			}},
	}
	for _, d := range defs {
		err := s.builtins.Register(builtins.Tool{
			ID: d.id, Group: builtins.GroupTriggers, Label: d.label, Description: d.desc, Unlisted: true, ReadOnly: d.readOnly,
			// The trigger host is set after the service is built, and only
			// when the operator left the triggers on.
			Enabled: func() bool { return s.triggers != nil },
			Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
				h := s.triggers
				connector := c.Origin == session.OriginConnector
				switch {
				case h == nil:
					return session.Tool{}, false, "triggers are off on this server"
				case c.SubAgent:
					return session.Tool{}, false, "a sub-agent cannot set triggers"
				case c.Origin != "" && !connector:
					return session.Tool{}, false, "a run that started by itself cannot set triggers"
				}
				if err := s.mayUseTriggers(ctx, c.Actor.Principal); err != nil {
					return session.Tool{}, false, err.Error()
				}
				desc, schema := d.desc, d.schema
				if connector {
					// A chat of a messaging app proposes and lists. A change or a
					// delete needs a card that only Studio shows, so it stays there.
					switch {
					case d.id == updateTriggerName || d.id == deleteTriggerName:
						return session.Tool{}, false, "change or delete a trigger in Studio"
					case c.AgentID == "":
						return session.Tool{}, false, "a trigger from a messaging app needs the agent of the connection"
					case !s.allowCap(ctx, CapabilityRequest{Capability: CapTriggersPropose, Binding: c.OriginID, Owner: c.Actor.Principal, Agent: c.AgentID}):
						return session.Tool{}, false, builtins.ReasonNotPermitted + ": " + BlockSay(CodeConnectionPermissionOff, CapTriggersPropose)
					}
					// The binding allows it; the sender of this turn must hold it too.
					if held, why := c.Holds(CapTriggersPropose); !held {
						return session.Tool{}, false, builtins.ReasonNotPermitted + ": " + why
					}
					if d.id == proposeTriggerName {
						desc, schema = desc+connectorProposeNote+h.actsNote(ctx, c)+h.channelsNote(ctx, c), proposeTriggerConnectorSchema
					}
				} else if d.id == updateTriggerName {
					desc += h.updateChannelsNote(ctx, c)
				}
				return session.Tool{Description: desc, Schema: json.RawMessage(schema), Run: d.run(h, c.Actor, c)}, true, ""
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ErrTriggersForbidden means the user does not have the triggers feature.
var ErrTriggersForbidden = errors.New("triggers are not enabled for this account")

// mayUseTriggers checks that the user may use triggers now: with accounts, the
// user has the triggers feature. It is checked at each turn, because a chat
// can outlive a permission.
func (s *Service) mayUseTriggers(ctx context.Context, p string) error {
	if s.d.Users == nil {
		return nil
	}
	u, err := s.d.Users(ctx, p)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrTriggersForbidden
	}
	if s.d.Allowed != nil && !s.d.Allowed(u, auth.FeatureTriggers) {
		return ErrTriggersForbidden
	}
	return nil
}

// The trigger tools of a chat of a scoped agent (a fenced chat) see and
// change only the triggers of that agent, and name folders as the chat does:
// a path of its place, which must be inside the agent's folder. A trigger of
// another agent, or of none, may reach the whole home; the person changes
// those on the Triggers page.

// toolScope is the folder that bounds the trigger tools of a chat: nil for a
// chat without a fence. A fenced chat whose agent's folder cannot be found
// gets an error, never the whole home.
func (h *Triggers) toolScope(ctx context.Context, a workspace.Actor, agentID string) (*folderScope, error) {
	if !workspace.HasFence(a) {
		return nil, nil
	}
	sc, err := h.svc.agentScope(ctx, a.Principal, agentID)
	if err == nil && sc == nil {
		// No agent, or it lost its folder since the turn began: the turn
		// is still fenced.
		err = ErrAgentFolderMissing
	}
	return sc, err
}

// toolFolder turns the folder that a model names into a path of the owner's
// home, as TriggerInput takes it. Without a scope it is a path of the home
// already.
func (h *Triggers) toolFolder(owner string, sc *folderScope, p string) (string, error) {
	if sc == nil {
		return p, nil
	}
	abs, err := sc.inFolder(p)
	if err != nil {
		return "", errOutsideFolder
	}
	rel := h.svc.d.Files.Rel(owner, abs)
	if rel == "" {
		return "", bad("a trigger can only watch a folder of your home")
	}
	return rel, nil
}

// toolTrigger finds the trigger that a model names, among the triggers the
// chat may see.
func (h *Triggers) toolTrigger(ctx context.Context, a workspace.Actor, agentID string, sc *folderScope, ref string) (triggers.Trigger, error) {
	if sc == nil {
		return h.ResolveTrigger(ctx, a.Principal, ref)
	}
	all, err := h.toolTriggers(ctx, a, agentID, sc)
	if err != nil {
		return triggers.Trigger{}, err
	}
	return resolveTriggerIn(all, ref)
}

// toolTriggers lists the triggers the chat may see.
func (h *Triggers) toolTriggers(ctx context.Context, a workspace.Actor, agentID string, sc *folderScope) ([]triggers.Trigger, error) {
	all, err := h.ListTriggers(ctx, a.Principal)
	if err != nil || sc == nil {
		return all, err
	}
	return slices.DeleteFunc(all, func(t triggers.Trigger) bool { return t.AgentID != agentID }), nil
}

func (h *Triggers) proposeTool(ctx context.Context, a workspace.Actor, c builtins.Chat, raw json.RawMessage) (string, error) {
	agentID, model := c.AgentID, c.Model
	var args struct {
		Name     string          `json:"name"`
		Prompt   string          `json:"prompt"`
		When     json.RawMessage `json:"when"`
		TimeZone string          `json:"time_zone"`
		DailyCap int             `json:"daily_cap"`
		RunLimit string          `json:"run_limit"`
		Reads    string          `json:"reads"`
		Writes   string          `json:"writes"`
		Widget   string          `json:"widget"`
		Tokens   int             `json:"estimated_tokens"`
		Access   string          `json:"access"`
		Acts     []string        `json:"acts"`
		Delivery *struct {
			Target  string `json:"target"`
			Mode    string `json:"mode"`
			Channel string `json:"channel"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	runLimit, rlErr := ParseRunLimit(args.RunLimit)
	if rlErr != nil {
		return "", rlErr
	}
	in := TriggerInput{Name: args.Name, Prompt: args.Prompt, DailyCap: args.DailyCap, RunLimit: runLimit, TZ: args.TimeZone,
		SourceChatID: a.ChatID, AgentID: agentID, Model: model,
		Reads: args.Reads, Writes: args.Writes, Widget: args.Widget, EstTokens: args.Tokens}
	var text string
	var obj struct {
		FileArrivesIn string `json:"file_arrives_in"`
		In            string `json:"in"`
		At            string `json:"at"`
	}
	switch {
	case json.Unmarshal(args.When, &text) == nil && text != "":
		in.Kind, in.Schedule = triggers.KindSchedule, text
	case json.Unmarshal(args.When, &obj) == nil && obj.FileArrivesIn != "":
		in.Kind, in.Folder, in.TZ = triggers.KindFile, obj.FileArrivesIn, ""
	case json.Unmarshal(args.When, &obj) == nil && (obj.In != "" || obj.At != ""):
		sched, zone, terr := timerSchedule(h.o.Clock(), obj.In, obj.At, args.TimeZone)
		if terr != nil {
			return "", terr
		}
		in.Kind, in.Schedule, in.TZ = triggers.KindSchedule, sched, zone
	default:
		return "", errors.New("when must be a schedule text, {\"in\": a delay}, {\"at\": a time of day} or {\"file_arrives_in\": folder}")
	}
	var err error
	connector := c.Origin == session.OriginConnector
	if connector {
		if in.Route, err = h.connectorRoute(ctx, c); err != nil {
			return "", err
		}
		if d := args.Delivery; d != nil {
			in.Route.Target, in.Route.Mode = d.Target, d.Mode
			if d.Target == triggers.TargetChannel {
				id, err := h.checkChannelTarget(ctx, c, d.Channel)
				if err != nil {
					return "", err
				}
				in.Route.Channel = id
			}
		}
		switch args.Access {
		case "", "read_write":
		case "read_only":
			in.Route.ReadOnly = true
		default:
			return "", errors.New(`access must be "read_only" or "read_write"`)
		}
		if in.Route.Target == "" {
			in.Route.Target = triggers.TargetInbox
			if in.Kind == triggers.KindSchedule && strings.HasPrefix(in.Schedule, "once ") {
				// A timer that a person sets in a chat is answered in the chat.
				in.Route.Target = triggers.TargetOrigin
			}
		}
	} else if args.Delivery != nil {
		return "", errors.New("delivery is for a chat in a messaging app; the answer of a run goes to the Inbox")
	}
	var dropped DroppedActs
	var may []string
	if connector {
		// The binding decides which acts a proposal from its chats may carry;
		// the agent must hold them too. What is beyond that is dropped and
		// named in the proposal, not refused.
		asked, aerr := checkProposedActs(args.Acts, "", nil, in.Route.ReadOnly)
		if aerr != nil {
			return "", toolErr(aerr)
		}
		ap := h.svc.connectors.ApprovalOf(ctx, in.Route.Binding)
		in.Acts, dropped = limitConnectorActs(asked, ap, c.Grants)
		may = requestableActs(ap, c.Grants)
	} else if in.Acts, err = checkProposedActs(args.Acts, agentID, c.Grants, in.Route.ReadOnly); err != nil {
		return "", toolErr(err)
	}
	sc, err := h.toolScope(ctx, a, agentID)
	if err != nil {
		return "", toolErr(err)
	}
	if in.Kind == triggers.KindFile {
		if in.Folder, err = h.toolFolder(a.Principal, sc, in.Folder); err != nil {
			return "", toolErr(err)
		}
	}
	t, fresh, err := h.propose(ctx, a.Principal, in)
	if err != nil {
		return "", errors.New(strings.TrimPrefix(err.Error(), ErrBadTrigger.Error()+": "))
	}
	warn := chatPathWarning(in.Prompt, in.Reads, in.Writes)
	if connector {
		out, err := h.afterPropose(ctx, a, t, fresh, in.Route, dropped, may)
		return out + warn, err
	}
	// The card lets the interface show Create, Edit and Dismiss on this tool
	// line, also after a reload; the event tells a client that is watching.
	session.SetToolCard(ctx, session.ToolCard{Kind: ProposalCardKind, ID: t.ID})
	h.svc.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindTriggerProposed, IDs: []string{t.ID}, Text: t.Name})
	return fmt.Sprintf("Proposed %q (%s). It is not active: the user must confirm it in the chat or on the Triggers page. Do not say it will run until they do.%s", t.Name, h.summary(t), warn), nil
}

func (h *Triggers) listTool(ctx context.Context, a workspace.Actor, agentID string, onlyAgent bool, raw json.RawMessage) (string, error) {
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
		RunLimit  string `json:"run_limit,omitempty"`
		Reads     string `json:"reads,omitempty"`
		Writes    string `json:"writes,omitempty"`
		Widget    string `json:"widget,omitempty"`
		EstTokens int    `json:"estimated_tokens,omitempty"`
		// Acts are shown so the model can tell the person; no tool changes them.
		Acts []string `json:"acts,omitempty"`
	}
	type row struct {
		Ref     string  `json:"ref"`
		Name    string  `json:"name"`
		When    string  `json:"when"`
		On      bool    `json:"on"`
		LastRun string  `json:"last_run,omitempty"`
		Detail  *detail `json:"detail,omitempty"`
	}
	sc, err := h.toolScope(ctx, a, agentID)
	if err != nil {
		return "", toolErr(err)
	}
	var all []triggers.Trigger
	switch {
	case args.Ref != "":
		t, err := h.toolTrigger(ctx, a, agentID, sc, args.Ref)
		if err != nil {
			return "", errors.New(strings.TrimPrefix(err.Error(), "studio: "))
		}
		if onlyAgent && t.AgentID != agentID {
			return "", errors.New("no such trigger")
		}
		all = []triggers.Trigger{t}
	case args.IncludePrompt:
		return "", errors.New("include_prompt needs the ref of one trigger")
	default:
		if all, err = h.toolTriggers(ctx, a, agentID, sc); err != nil {
			return "", err
		}
	}
	if onlyAgent {
		// A chat of a messaging app is open to more people than the owner: it
		// lists only what its own agent runs.
		all = slices.DeleteFunc(all, func(t triggers.Trigger) bool { return t.AgentID != agentID })
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
			r.Detail = &detail{Prompt: t.Prompt, Schedule: t.Schedule, TimeZone: t.TZ, Folder: h.RelFolder(t), DailyCap: t.DailyCap, RunLimit: runLimitText(t.RunLimit),
				Reads: t.Reads, Writes: t.Writes, Widget: t.Widget, EstTokens: t.EstTokens, Acts: t.Acts}
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

func (h *Triggers) updateTool(ctx context.Context, a workspace.Actor, c builtins.Chat, raw json.RawMessage) (string, error) {
	agentID := c.AgentID
	var args struct {
		Ref      string          `json:"ref"`
		Name     *string         `json:"name"`
		Prompt   *string         `json:"prompt"`
		When     json.RawMessage `json:"when"`
		TimeZone *string         `json:"time_zone"`
		DailyCap *int            `json:"daily_cap"`
		RunLimit *string         `json:"run_limit"`
		Enabled  *bool           `json:"enabled"`
		Reads    *string         `json:"reads"`
		Writes   *string         `json:"writes"`
		Widget   *string         `json:"widget"`
		Tokens   *int            `json:"estimated_tokens"`
		Acts     *[]string       `json:"acts"`
		Delivery *struct {
			Target  string `json:"target"`
			Mode    string `json:"mode"`
			Channel string `json:"channel"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	sc, err := h.toolScope(ctx, a, agentID)
	if err != nil {
		return "", toolErr(err)
	}
	target, err := h.toolTrigger(ctx, a, agentID, sc, args.Ref)
	if err != nil {
		return "", toolErr(err)
	}
	var runLimit *time.Duration
	if args.RunLimit != nil {
		d, err := ParseRunLimit(*args.RunLimit)
		if err != nil {
			return "", err
		}
		runLimit = &d
	}
	p := TriggerPatch{Name: args.Name, Prompt: args.Prompt, TZ: args.TimeZone, DailyCap: args.DailyCap, RunLimit: runLimit, Enabled: args.Enabled,
		Reads: args.Reads, Writes: args.Writes, Widget: args.Widget, EstTokens: args.Tokens}
	if args.Acts != nil {
		acts, err := h.checkChangedActs(ctx, target, *args.Acts)
		if err != nil {
			return "", toolErr(err)
		}
		p.Acts = &acts
	}
	if d := args.Delivery; d != nil {
		if p.Delivery, err = h.checkChangedDelivery(ctx, c, target, d.Target, d.Mode, d.Channel); err != nil {
			return "", toolErr(err)
		}
	}
	if len(args.When) > 0 && string(args.When) != "null" {
		var text string
		var obj struct {
			FileArrivesIn string `json:"file_arrives_in"`
		}
		switch {
		case json.Unmarshal(args.When, &text) == nil && text != "":
			p.Kind, p.Schedule = triggers.KindSchedule, text
		case json.Unmarshal(args.When, &obj) == nil && obj.FileArrivesIn != "":
			folder, err := h.toolFolder(a.Principal, sc, obj.FileArrivesIn)
			if err != nil {
				return "", toolErr(err)
			}
			p.Kind, p.Folder = triggers.KindFile, folder
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
	return fmt.Sprintf("Asked to change %q (%s). Nothing is changed yet: the user must press Apply on the card in the chat. Do not say it changed until they do.%s",
		target.Name, strings.Join(changedFields(target, asTrigger(rec)), ", "), chatPathWarning(rec.Prompt, rec.Reads, rec.Writes)), nil
}

func (h *Triggers) deleteTool(ctx context.Context, a workspace.Actor, agentID string, raw json.RawMessage) (string, error) {
	var args struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	sc, err := h.toolScope(ctx, a, agentID)
	if err != nil {
		return "", toolErr(err)
	}
	target, err := h.toolTrigger(ctx, a, agentID, sc, args.Ref)
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

func (h *Triggers) summary(t triggers.Trigger) string { return h.summaryIn(hostmsg.Voice{}, t) }

// summaryIn is summary in the language of the voice.
func (h *Triggers) summaryIn(v hostmsg.Voice, t triggers.Trigger) string {
	if t.Kind == triggers.KindFile {
		return v.T("when.files", "folder", h.RelFolder(t))
	}
	sch, err := triggers.ParseSchedule(t.Schedule)
	if err != nil {
		return t.Schedule
	}
	s := scheduleWords(v, sch)
	if t.TZ != "" {
		s += " (" + t.TZ + ")"
	}
	return s
}

// scheduleWords words a schedule in the language of the voice. For English it
// is the same text as Schedule.String.
func scheduleWords(v hostmsg.Voice, s triggers.Schedule) string {
	p := s.Part()
	hm := fmt.Sprintf("%02d:%02d", p.Hour, p.Minute)
	switch p.Kind {
	case "":
		return v.T("when.never")
	case "daily":
		return v.T("when.daily", "time", hm)
	case "weekdays":
		return v.T("when.weekdays", "time", hm)
	case "weekly":
		days := make([]string, len(p.Weekdays))
		for i, d := range p.Weekdays {
			days[i] = v.T("day." + strconv.Itoa(d))
		}
		return v.T("when.weekly", "days", v.Join(days), "time", hm)
	case "monthly":
		return v.T("when.monthly", "day", p.Day, "time", hm)
	case "once":
		return v.T("when.once", "date", fmt.Sprintf("%04d-%02d-%02d", p.Year, p.Month, p.DayOfMonth), "time", hm)
	}
	return v.T("when.cron", "text", p.Text)
}

// connectorRoute is the place a trigger proposed in the connector chat c comes
// from: the binding, and the chat and thread where the person wrote. A chat
// with no known place cannot propose, since the proposal could not be shown.
func (h *Triggers) connectorRoute(ctx context.Context, c builtins.Chat) (triggers.Route, error) {
	conns := h.svc.connectors
	if conns == nil {
		return triggers.Route{}, errors.New("connections to messaging apps are off on this server")
	}
	chat, thread, ok := conns.PlaceOf(ctx, c.OriginID, c.Actor.ChatID)
	if !ok {
		return triggers.Route{}, errors.New("I cannot tell where this conversation is written, so I cannot show a proposal here; ask the owner to propose it in Studio")
	}
	return triggers.Route{Binding: c.OriginID, Chat: chat, Thread: thread}, nil
}

// channelsNote names the channels that delivery.channel accepts here, and says
// where each target writes.
func (h *Triggers) channelsNote(ctx context.Context, c builtins.Chat) string {
	conns := h.svc.connectors
	if conns == nil {
		return ""
	}
	b, err := conns.o.Store.GetBinding(ctx, c.OriginID)
	if err != nil {
		return ""
	}
	holds, _ := c.Holds(contract.CapTalkbackChannels)
	note := channelsNote(b, holds, "delivery.target channel posts in the channel itself, at the top and not in a thread; origin posts in this thread. "+
		"Name the channel in delivery.channel by its name or its ID, and use channel only when the person asks for the results there.")
	if holds {
		note += notApprovedText(b)
	}
	return note
}

// actsNote tells the model which acts it may ask for from this connection, and
// when to ask the person. It is added to the description of propose_trigger.
func (h *Triggers) actsNote(ctx context.Context, c builtins.Chat) string {
	may := requestableActs(h.svc.connectors.ApprovalOf(ctx, c.OriginID), c.Grants)
	if len(may) == 0 {
		return " The owner allows no acts for this connection: leave acts out; runs only read and report."
	}
	return " Acts you may ask for here: " + strings.Join(may, ", ") + ". Ask for one only when the job needs it " +
		"(a run that executes commands needs act.addons). When the job clearly changes things, first ask the person: only report, or also act?"
}

// announceProposal writes the proposal in the chat it came from, with its code,
// and words the result for the model.
func (h *Triggers) announceProposal(ctx context.Context, a workspace.Actor, t triggers.Trigger, here triggers.Route, note string, dr DroppedActs, may []string) (string, error) {
	h.svc.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindTriggerProposed, IDs: []string{t.ID}, Text: t.Name})
	if t.Route.Binding != here.Binding || t.Route.Chat != here.Chat || t.Route.Thread != here.Thread {
		// The same proposal was made from another place: its code works there.
		return fmt.Sprintf("A proposal like %q waits already, from another conversation. It is not active. Tell the person to confirm it in Studio.", t.Name), nil
	}
	actsNote := actsResultNote(t, dr, may)
	if h.announce(ctx, t, note, dr) {
		return fmt.Sprintf("Proposed %q (%s). It is not active. The host wrote the proposal and its code in this chat; the person answers there with confirm or cancel and the code, or in Studio. "+
			"Say in one short sentence that you proposed it; do not repeat the code and do not say it will run until they confirm.%s", t.Name, h.summary(t), actsNote), nil
	}
	return fmt.Sprintf("Proposed %q (%s). It is not active. The host could not write the code in this chat. Tell the person to reply \"confirm %s\" or \"cancel %s\" here, or to open Studio. "+
		"Do not say it will run until they confirm.%s", t.Name, h.summary(t), t.Route.Code, t.Route.Code, actsNote), nil
}

// actsResultNote tells the model, in the result of propose_trigger, which acts
// the proposal carries, which were dropped, and which it may ask for in this
// chat. The model must not promise the person more than this.
func actsResultNote(t triggers.Trigger, dr DroppedActs, may []string) string {
	held := effectiveActs(t)
	note := " Acts on the proposal: "
	if t.Route.ReadOnly || len(held) == 0 {
		note += "none, so its runs only read and report."
	} else {
		note += strings.Join(held, ", ") + "."
	}
	if !dr.Empty() {
		note += " Dropped, and named in the proposal: " + strings.Join(append(slices.Clone(dr.Binding), dr.Agent...), ", ") + "."
	}
	if len(may) > 0 {
		note += " Acts you may ask for in this chat: " + strings.Join(may, ", ") + "."
	} else {
		note += " This chat allows no acts: leave acts out."
	}
	return note
}

// ParseRunLimit reads the run_limit of a trigger: a duration text such as 30m.
// Empty and 0 mean the default and give zero. The bounds are checked when the
// trigger is validated.
func ParseRunLimit(text string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("run_limit must be a duration such as 30m or 2h, or empty for the default")
	}
	return d, nil
}

// runLimitText is a run limit as the text a person or a model reads back; zero
// gives the empty text.
func runLimitText(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return limitText(d)
}
