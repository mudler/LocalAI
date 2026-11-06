package studio

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
)

// The switch groups of the tools that the chats build with their own kind:
// the file tools, the image and audio helpers, the collection tools and
// spawn_agent. They are in the registry of built-in tools as External tools,
// so the Tools menu, PUT .../builtins and the draft list know them like the
// web or the trigger tools, while the code that builds them keeps building
// them (with the wrappers they need for a turn) and drops the ones the chat
// switched off (Registry.Hidden). bash is in GroupShell as the built-in tool
// it already was, and its switch also hides the Sandbox add-on's bash
// (builtins.Tool.Covers).
//
// They are on until the chat switches them off. A sub-agent follows the
// switches of its parent chat: it never has a tool its parent switched off.

// The file tools, in the group GroupFiles. read_document comes from the media
// helpers but reads a file like read_file does, so it switches with them.
var fileToolNames = []string{"list_files", "read_file", "write_file", "edit_file", "move_file", "delete_file", "search_files", "read_document"}

// The helper-model tools, in the group GroupMedia.
var mediaToolNames = []string{"describe_image", "transcribe_audio"}

// The collection tools, in the group GroupCollections.
var collectionToolNames = []string{"list_collections", "search_collection"}

// spawnAgentName is the tool that starts a sub-agent, in GroupSubAgents. wait_agents follows its switch.
const spawnAgentName = "spawn_agent"

// ErrSubAgentsOff means the chat switched the sub-agents off.
var ErrSubAgentsOff = errors.New("sub-agents are off for this chat; the user can switch them on in the Tools menu")

// registerToolGroups adds the file tools, the helper-model tools, the
// collection tools and spawn_agent to the registry, each in its switch
// group. They have no runtime setting of their own (Settings > Tools does not
// list them): what the server offers decides whether a group exists at all.
func (s *Service) registerToolGroups() error {
	type def struct {
		ids          []string
		group, label string
		desc         string
		build        func(ctx context.Context, c builtins.Chat) (bool, string)
	}
	filesOn := func(context.Context, builtins.Chat) (bool, string) {
		if !s.d.Files.Available() {
			return false, "the files are off"
		}
		return true, ""
	}
	var defs []def
	if s.d.Files.Available() {
		defs = append(defs, def{fileToolNames, builtins.GroupFiles, "Your files",
			"List, read, write, edit, move, delete and search the user's files, and read documents.", filesOn})
		if s.d.Media != nil {
			defs = append(defs, def{mediaToolNames, builtins.GroupMedia, "Images and audio",
				"Describe an image or transcribe a recording of the user's files with a helper model.", filesOn})
		}
	}
	if s.d.Collections != nil {
		defs = append(defs, def{collectionToolNames, builtins.GroupCollections, "Collections",
			"List and search the user's collections.",
			func(ctx context.Context, c builtins.Chat) (bool, string) {
				if !s.d.Collections.Allowed(ctx, c.Actor.Principal) {
					return false, "collections are not enabled for this account"
				}
				return true, ""
			}})
	}
	defs = append(defs, def{[]string{spawnAgentName}, builtins.GroupSubAgents, "Sub-agents",
		"Start a sub-agent that works on a task and reports back.",
		func(_ context.Context, c builtins.Chat) (bool, string) {
			if c.SubAgent {
				return false, ErrSubAgentDepth.Error()
			}
			return true, ""
		}})
	for _, d := range defs {
		for _, id := range d.ids {
			err := s.builtins.Register(builtins.Tool{
				ID: id, Group: d.group, Label: d.label, Description: d.desc, External: true, Unlisted: true,
				Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
					ok, reason := d.build(ctx, c)
					return session.Tool{}, ok, reason
				},
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// switchesOf returns the groups a chat switched off and on. A sub-agent has
// none of its own: it follows its parent chat.
//
// The groups the chat's saved agent turns off are off whatever the chat says:
// they are the agent's ceiling, and a sub-agent follows the ceiling of the
// agent of its parent.
func (s *Service) switchesOf(ctx context.Context, rec session.ChatRecord) (off, on []string) {
	if rec.Kind == session.KindSubAgent && rec.ParentID != "" {
		parent, err := s.d.Store.GetChat(ctx, rec.Principal, rec.ParentID)
		if err == nil {
			return mergeGroups(slices.Clone(parent.BuiltinsOff), s.agentOff(ctx, parent)), parent.BuiltinsOn
		}
		s.log.Warn("studio: the parent of a sub-agent is gone; it keeps every group off", "chat", rec.ID, "error", err)
		return s.builtins.Groups(), nil
	}
	on = rec.BuiltinsOn
	if rec.Assistant {
		// Assistant mode is a convenience: it switches the admin group on for
		// this chat. The chat's own switch and the agent's ceiling still win.
		on = mergeGroups(slices.Clone(on), []string{builtins.GroupLocalAIAdmin})
	}
	return mergeGroups(slices.Clone(rec.BuiltinsOff), s.agentOff(ctx, rec)), on
}

// agentOff are the groups of built-in tools the saved agent of the chat turns
// off, or none for a chat without an agent or an agent that cannot be read
// (the scope of a missing agent already leaves the chat without tools).
func (s *Service) agentOff(ctx context.Context, rec session.ChatRecord) []string {
	if rec.AgentID == "" || s.d.Agents == nil {
		return nil
	}
	ag, err := s.chatAgent(ctx, rec.Principal, rec.AgentID)
	if err != nil {
		return nil
	}
	return ag.BuiltinsOff
}

// hiddenFor returns the names of the tools the chat's switches take away
// that the registry does not build: see builtins.Registry.Hidden.
func (s *Service) hiddenFor(ctx context.Context, rec session.ChatRecord) map[string]bool {
	off, on := s.switchesOf(ctx, rec)
	return s.builtins.Hidden(builtins.Chat{GroupsOff: off, GroupsOn: on})
}

// dropHidden removes the tools of the hidden names, and the stand-in of an
// add-on that cannot serve a hidden name: a chat that switched bash off
// does not hear that the Sandbox cannot run.
func dropHidden(tools []session.Tool, hidden map[string]bool) []session.Tool {
	if len(hidden) == 0 {
		return tools
	}
	out := tools[:0:0]
	for _, t := range tools {
		if hidden[t.Name] || slices.ContainsFunc(t.StandInFor, func(n string) bool { return hidden[n] }) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// shellSwitched is the state of a chat that switched the shell group off:
// no tool runs commands, whoever would serve them.
func shellSwitched(c Commands, hidden map[string]bool) Commands {
	if !hidden[commandTool] {
		return c
	}
	return Commands{State: CommandsSwitchedOff}
}

// shellReason is why the shell group cannot serve a chat, from what the
// chat's add-ons say about running commands: the same facts as the
// notice of the Tools menu. buildReason is what the built-in bash said.
func shellReason(c Commands, buildReason string) string {
	switch c.State {
	case CommandsOff:
		return commandsPhrase(c) + " is off for this chat"
	case CommandsUnavailable:
		if c.Reason != "" {
			return commandsPhrase(c) + " cannot run commands here: " + c.Reason
		}
		return commandsPhrase(c) + " cannot run commands here"
	}
	if buildReason != "" && buildReason != builtins.ReasonOff {
		return buildReason
	}
	return "nothing here runs commands: no add-on that runs commands is installed and commands on this machine are off"
}

// filesNote is the line under the chat's file list when the chat has no
// way to open them: no file tool and no command.
const filesNote = "The file tools are switched off for this chat, so you cannot open or change these files. Say so when a task needs them."

// canReadFiles says whether a chat's tools can read its files: a file tool,
// or a tool that runs commands.
func canReadFiles(tools []session.Tool, c Commands) bool {
	return hasTool(tools, "read_file") || c.State == CommandsAvailable
}

// canWriteFiles says whether a chat's tools can save a file.
func canWriteFiles(tools []session.Tool, c Commands) bool {
	return hasTool(tools, "write_file") || c.State == CommandsAvailable
}

// withoutWriteHint takes the sentence about writing skills out of the skill
// index of a chat that cannot save a file.
func withoutWriteHint(index string) string {
	return strings.Replace(index, " "+skills.WriteHint, "", 1)
}
