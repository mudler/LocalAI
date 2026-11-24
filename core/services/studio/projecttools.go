package studio

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The tool list_projects lets the model name one of the person's projects. It
// is the group builtins.GroupProjects, on until the chat switches it off, as
// the collections group is. It only reads, and it is built only for a chat a
// person watches: a sub-agent, a run that starts by itself and a messaging chat
// (where the sender may not be the owner) do not get it.
//
// There is no pull_project tool. A pull mounts another project for the chat,
// and the studio has no approval step for a mount that the model asks for (a
// change proposal asks the person to confirm a text change, and a card to
// confirm a mount would be a new mechanism). The person pulls a project in
// from the chat's menu or the API. The model in a chat may not pull either:
// guardProjects refuses pull, unpull, set_chat_project and delete there.

const listProjectsName = "list_projects"

const listProjectsDescription = "List the person's active projects (id, name, slug, description). Use it when they name a project or ask about one."

const listProjectsSchema = `{"type":"object","properties":{}}`

var errProjectsOff = errors.New("projects are not available on this server")

// registerProjectTools adds list_projects to the registry.
func (s *Service) registerProjectTools() error {
	return s.builtins.Register(builtins.Tool{
		ID: listProjectsName, Group: builtins.GroupProjects, Label: "Projects", ReadOnly: true, Unlisted: true,
		Description: "Let the model list your active projects.",
		Build: func(_ context.Context, c builtins.Chat) (session.Tool, bool, string) {
			switch {
			case s.d.Projects == nil:
				return session.Tool{}, false, errProjectsOff.Error()
			case c.SubAgent || c.Origin != "":
				return session.Tool{}, false, "a sub-agent, a run that starts by itself and a messaging chat cannot list the person's projects"
			}
			return session.Tool{Description: listProjectsDescription, Schema: json.RawMessage(listProjectsSchema),
				Run: func(ctx context.Context, _ json.RawMessage) (string, error) { return s.listProjectsTool(ctx, c) }}, true, ""
		},
	})
}

type listedProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// listProjectsTool is list_projects. It reads the projects of the chat's
// owner, in the index and the manifests, and returns the active ones.
func (s *Service) listProjectsTool(ctx context.Context, c builtins.Chat) (string, error) {
	if s.d.Projects == nil {
		return "", errProjectsOff
	}
	owner := c.Actor.Principal
	list, err := s.d.Projects.List(ctx, owner)
	if err != nil {
		return "", err
	}
	// A chat inside a fence sees its own project only: the names and
	// descriptions of the others stay out, as for the projects tool.
	fenced, own := false, ""
	if s.d.Store != nil {
		if rec, err := s.getChatEffective(ctx, owner, c.Actor.ChatID); err != nil || s.chatFenced(ctx, rec) {
			// An unreadable chat cannot be shown to be outside a fence.
			fenced = true
			if err == nil {
				own = s.ownProjectID(ctx, rec)
			}
		}
	}
	out := struct {
		Projects []listedProject `json:"projects"`
	}{Projects: []listedProject{}}
	for _, p := range list {
		if p.Status != projects.StatusActive || (fenced && p.ID != own) {
			continue
		}
		lp := listedProject{ID: p.ID, Name: p.Name, Slug: p.Slug}
		if r, err := s.d.Projects.Get(ctx, owner, p.ID); err == nil {
			lp.Description = r.Manifest.Description
		}
		out.Projects = append(out.Projects, lp)
	}
	b, err := json.Marshal(out)
	return string(b), err
}

var (
	errProjectsMessaging = errors.New("projects is not available in a messaging chat or a sub-agent: the person writing may not be the owner of the projects")
	errProjectsFenced    = errors.New("projects can only get its own project and show its agents, connections, triggers, loops, chats and memory in this chat: it works inside a fence, and other projects stay out of it. Say that a person must change projects in a chat they open without this agent")
	errProjectsUnattend  = errors.New("projects cannot change anything in a run that starts by itself: nobody watches it. Say that a person must do it in a chat they watch")
)

// modelRefusedProjectActions are the actions the model in an interactive chat
// may not run: they mount or move or delete, and a pull is the owner's
// approval to give, which the studio has no step for. The person does them on
// the Projects page, and API and MCP callers keep them (this gate is on the
// chat side only).
var modelRefusedProjectActions = []string{localaitools.ActionPull, localaitools.ActionUnpull, localaitools.ActionSetChatProject, localaitools.ActionDelete}

// projectsUnattendedReads are the actions a run that starts by itself may use.
// The shared projects of other people are not here: they are outside the
// fence of such a run.
var projectsUnattendedReads = []string{localaitools.ActionList, localaitools.ActionGet, localaitools.ActionAgents,
	localaitools.ActionConnections, localaitools.ActionTriggers, localaitools.ActionLoops, localaitools.ActionChats, localaitools.ActionMemory}

var errProjectsOwnOnly = errors.New("in this chat projects reads only the project this chat works for: it works inside a fence, and other projects stay out of it, with their names and descriptions. Ask a person to open them in a chat without this fence")

// errProjectsMemoryPropose is the refusal of write_memory in a chat: the model
// saves a project note through the proposal the person confirms.
var errProjectsMemoryPropose = errors.New("the model does not write the project memory with this tool: use propose_change with scope project and target set to the file, and the person confirms the note")

// projectsOwnOnlyReads are the reads of one project. A chat that works inside
// a fence or starts by itself may read them for its own project only: another
// project's name, description, defaults, trigger folders and schedules, loop
// prompts, chat titles, agents and memory stay out. list is cut to the own
// project (see ownOnlyList).
var projectsOwnOnlyReads = []string{localaitools.ActionGet, localaitools.ActionAgents, localaitools.ActionConnections,
	localaitools.ActionTriggers, localaitools.ActionLoops, localaitools.ActionChats, localaitools.ActionMemory}

// errProjectsAttachCredentials is the refusal of an attach of a connector, an
// add-on or an MCP server in a chat. Such a tool can hold the owner's
// credentials, and the studio has no step in a chat for the person to confirm
// it, so the person attaches it on the project page.
var errProjectsAttachCredentials = errors.New("the model may not attach a connector, an add-on or an MCP server to a project: they can hold credentials. Ask the person to attach them on the project page. Skills and collections can be attached here")

// attachedHoldsCredentials reads the attached argument as the tool does. An
// argument that does not read is treated as holding credentials: the gate
// fails closed.
func attachedHoldsCredentials(m map[string][]string) bool {
	a, err := localaitools.ProjectAttachedFromMap(m)
	return err != nil || a.HoldsCredentials()
}

var errProjectsAttachRead = errors.New("could not read what the project attaches, so the attached lists were not changed")

var errProjectsPerson = errors.New("the model may not pull, unpull, move a chat to a project or delete a project: ask the person to do it on the Projects page")

// guardProjects limits the projects client tool by the kind of chat. It
// reaches a person's projects and their chats, so a messaging chat and a
// sub-agent get none of it, a run that starts by itself and a fenced chat (a
// project agent, or any agent with a folder) only read (list, get and
// agents), and an interactive chat that is not fenced may not pull, unpull,
// set the chat's project or delete. A fenced chat would otherwise change the
// owner's other projects from inside its fence.
//
// A fenced chat and a run that starts by itself also read the project detail
// actions (get, agents, connections, triggers, loops, chats, memory) of their
// own project only, and list shows only that project. own returns the ID of
// that project, read when the tool runs, or "" when the chat has none; with
// none, those actions are refused and list is empty.
func guardProjects(tools []session.Tool, chat builtins.Chat, fenced bool, own func(context.Context) string) []session.Tool {
	for i, tool := range tools {
		if tool.Name != localaitools.ToolProjects {
			continue
		}
		inner := tool.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			if chat.SubAgent || chat.Origin == session.OriginConnector {
				return "", errProjectsMessaging
			}
			cut := false
			if chat.Origin != "" {
				var in localaitools.ProjectsToolInput
				if json.Unmarshal(args, &in) != nil || !slices.Contains(projectsUnattendedReads, in.Action) {
					return "", errProjectsUnattend
				}
				if err := ownProjectOnly(ctx, in, own); err != nil {
					return "", err
				}
				cut = in.Action == localaitools.ActionList
			}
			if chat.Origin == "" && fenced {
				var in localaitools.ProjectsToolInput
				if json.Unmarshal(args, &in) != nil || !slices.Contains(projectsUnattendedReads, in.Action) {
					return "", errProjectsFenced
				}
				if err := ownProjectOnly(ctx, in, own); err != nil {
					return "", err
				}
				cut = in.Action == localaitools.ActionList
			}
			if chat.Origin == "" {
				// An unreadable argument list cannot be shown to be safe, so it
				// is refused as well: the gate fails closed.
				var in localaitools.ProjectsToolInput
				if json.Unmarshal(args, &in) != nil || slices.Contains(modelRefusedProjectActions, in.Action) {
					return "", errProjectsPerson
				}
				if in.Action == localaitools.ActionWriteMemory {
					return "", errProjectsMemoryPropose
				}
				if (in.Action == localaitools.ActionCreate || in.Action == localaitools.ActionUpdate) && attachedHoldsCredentials(in.Attached) {
					return "", errProjectsAttachCredentials
				}
				if in.Action == localaitools.ActionUpdate && in.Attached != nil {
					merged, err := mergeAttachedArgs(ctx, inner, in)
					if err != nil {
						return "", err
					}
					args = merged
				}
			}
			out, err := inner(ctx, args)
			if cut && err == nil {
				id := ""
				if own != nil {
					id = own(ctx)
				}
				return ownOnlyList(out, id)
			}
			return out, err
		}
	}
	return tools
}

// mergeAttachedArgs is the arguments of an update from a chat with the attached
// block merged into the block the project has: the entries the model sends are
// added and nothing is removed. The tool says it replaces the block, and a
// model that lists only a skill would detach the connectors, add-ons and
// servers that the person attached; the person detaches on the project page.
// The project is read first; a read that fails refuses the update.
func mergeAttachedArgs(ctx context.Context, inner func(context.Context, json.RawMessage) (string, error), in localaitools.ProjectsToolInput) (json.RawMessage, error) {
	get, _ := json.Marshal(localaitools.ProjectsToolInput{Action: localaitools.ActionGet, Project: in.Project})
	out, err := inner(ctx, get)
	if err != nil {
		return nil, err
	}
	var res localaitools.ProjectsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Project == nil {
		return nil, errProjectsAttachRead
	}
	cur := res.Project.Attached
	merged := map[string][]string{
		"skills": cur.Skills, "addons": cur.Addons, "connectors": cur.Connectors,
		"mcp_servers": cur.MCPServers, "collections": cur.Collections,
	}
	for k, v := range in.Attached {
		merged[k] = mergeNames(merged[k], v)
	}
	in.Attached = merged
	return json.Marshal(in)
}

// ownOnlyList keeps only the own project in the answer of list. With no own
// project the list is empty.
func ownOnlyList(out, own string) (string, error) {
	var res localaitools.ProjectsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		// An answer that cannot be read cannot be shown to be safe.
		return "", errProjectsOwnOnly
	}
	kept := []localaitools.ProjectInfo{}
	for _, p := range res.Projects {
		if own != "" && p.ID == own {
			kept = append(kept, p)
		}
	}
	res.Projects = kept
	b, err := json.Marshal(res)
	return string(b), err
}

// ownProjectOnly refuses a detail read of a project that is not the chat's own.
func ownProjectOnly(ctx context.Context, in localaitools.ProjectsToolInput, own func(context.Context) string) error {
	if !slices.Contains(projectsOwnOnlyReads, in.Action) {
		return nil
	}
	if own == nil || in.Project == "" || own(ctx) != in.Project {
		return errProjectsOwnOnly
	}
	return nil
}
