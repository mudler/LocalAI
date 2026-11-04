package studio

import "github.com/mudler/LocalAI/core/services/studio/builtins"

// describeGroups declares the title and the description of every switch group
// of built-in tools. The Tools menu of a chat and the editor of a saved agent
// show them next to each switch, so a person can tell what the model may do
// when it is on. A description says what the model can do, in the words of the
// tools of the group; the descriptions of the tools themselves are written for
// the model and stay as they are. A group the server does not offer is
// described all the same: the registry only lists the groups it has.
func (s *Service) describeGroups() {
	for group, info := range map[string]builtins.GroupInfo{
		builtins.GroupShell: {
			Title: "Run commands",
			Description: "The model can run shell commands and scripts. They run in an add-on that isolates them when one is installed, " +
				"otherwise directly on this machine.",
			WhenOff: "The model can write files but cannot run anything.",
		},
		builtins.GroupFiles: {
			Title: "Your files",
			Description: "The model can list, read, write, edit, move, delete and search your files, and read documents. " +
				"A chat of an agent that works in one folder reaches only that folder.",
			WhenOff: "The model cannot see or change your files.",
		},
		builtins.GroupWeb: {
			Title:       "Look things up online",
			Description: "The model can search the web and read a public page by its address. Addresses inside your network are refused unless an administrator allows them.",
			WhenOff:     "The model cannot look anything up online.",
		},
		builtins.GroupMedia: {
			Title:       "Images and audio",
			Description: "The model can describe an image or transcribe a recording from your files with a helper model.",
			WhenOff:     "The model cannot read images or recordings itself.",
		},
		builtins.GroupCollections: {
			Title:       "Collections",
			Description: "The model can list your collections and search them for the passages that match a question.",
			WhenOff:     "The model cannot read your collections.",
		},
		builtins.GroupSubAgents: {
			Title:       "Sub-agents",
			Description: "The model can start a sub-agent that works on one task with the same switches as this chat and reports back.",
			WhenOff:     "The model works alone.",
		},
		builtins.GroupSavedAgents: {
			Title:       "Saved agents",
			Description: "The model can list your saved agents and start one as a sub-agent, with that agent's own instructions and model.",
			WhenOff:     "The model cannot see or start your saved agents.",
		},
		builtins.GroupTriggers: {
			Title: "Triggers",
			Description: "The model can propose, list, change and delete triggers: tasks that run by themselves at a set time or when a file arrives in a folder. " +
				"A new trigger or a change waits on a card in the chat until you confirm it; pausing or resuming a trigger applies at once and you can undo it.",
			WhenOff: "The model cannot create or manage triggers. A run that a trigger or a connector started never has them.",
		},
		builtins.GroupChanges: {
			Title: "Memory and profile changes",
			Description: "The model can propose an edit to your memory notes or to an agent's standing rules, and undo an edit you confirmed. " +
				"Nothing changes until you confirm the proposal on a card in the chat.",
			WhenOff: "The model cannot propose edits to your memory or to an agent's rules.",
		},
	} {
		s.builtins.DescribeGroup(group, info)
	}
}
