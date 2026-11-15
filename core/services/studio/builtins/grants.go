package builtins

import (
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A grant is a right that the owner gives a saved agent on top of its lists:
// to ask for a change of the owner's own state, to set up a trigger, to reach
// a channel. Grants are the Builtins of an agent. None is on by default, and a
// grant that is sensitive is marked so that every screen that offers it can
// say so. The declarations live here, in code, so the core names no add-on and
// the editor and the chat read one list.
//
// The names are the capability names of the contract where one exists, and
// the opt-in group saved_agents.

// The grant names.
const (
	GrantMemoryWrite      = contract.CapMemoryWrite
	GrantProfileWrite     = contract.CapProfileWrite
	GrantSkillsWrite      = contract.CapSkillsWrite
	GrantPagesWrite       = contract.CapPagesWrite
	GrantDashboardWrite   = contract.CapDashboardWrite
	GrantKnowledgeWrite   = contract.CapKnowledgeWrite
	GrantTriggersPropose  = contract.CapTriggersPropose
	GrantTriggersRun      = contract.CapTriggersRun
	GrantTalkbackChannels = contract.CapTalkbackChannels
	GrantTalkbackNewChan  = contract.CapTalkbackNewChan
	GrantFilesUpload      = contract.CapFilesUpload
	GrantActFiles         = "act.files"
	GrantActAddons        = "act.addons"
	GrantActSend          = "act.send"
	GrantActSpend         = "act.spend"
	// GrantSavedAgents is the opt-in group GroupSavedAgents.
	GrantSavedAgents = GroupSavedAgents
	// GrantHistoryRead lets a saved agent search and read the owner's earlier
	// chats (the group GroupHistory). A run that starts by itself, a
	// sub-agent and a messaging chat never hold it.
	GrantHistoryRead = "history.read"
)

// Grant describes one grant for a person: a title and what the agent can do
// with it. Both are English; the interface may translate them by Name.
type Grant struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Sensitive grants change what the agent may do or reach later (a rule, a
	// skill, a page, an action): they are never on by default and the editor
	// marks them.
	Sensitive bool `json:"sensitive"`
}

// grants is the closed list, in the order a screen shows it.
var grants = []Grant{
	{GrantMemoryWrite, "Remember things", "The agent can propose a note in your memory, or in its own. You confirm each note.", false},
	{GrantProfileWrite, "Change its own rules", "The agent can propose an edit to its profile, the standing rules it follows in every chat. You confirm each edit.", true},
	{GrantSkillsWrite, "Write skills", "The agent can propose a new or changed skill for itself or for you. You confirm each one, and a skill you locked stays as it is.", true},
	{GrantPagesWrite, "Create pages", "The agent can propose a page: a small app that opens in a sandboxed frame. You see its files and confirm.", true},
	{GrantDashboardWrite, "Change the dashboard", "The agent can propose a new layout of your Home dashboard. You confirm each change.", true},
	{GrantKnowledgeWrite, "Add to knowledge", "The agent can propose a text for one of its collections. The host indexes it only after you confirm, and never at once.", true},
	{GrantTriggersPropose, "Propose triggers", "The agent can propose a task that runs by itself, from a messaging app too. You confirm it.", false},
	{GrantTriggersRun, "Run triggers", "The triggers of this agent may start from a messaging app.", false},
	{GrantTalkbackChannels, "Reply on known channels", "The agent can send a message to a channel it already talks in.", false},
	{GrantTalkbackNewChan, "Start new channels", "The agent can start a conversation on a channel it has not used before.", true},
	{GrantFilesUpload, "Receive files", "In a chat that a messaging app starts, a sender can attach files for the agent to read. It does not let the agent send files out.", false},
	{GrantActFiles, "Write files on its own", "A run that nobody watches may write files inside the agent's folders.", true},
	{GrantActAddons, "Use add-on actions on its own", "A run that nobody watches may call the actions of the add-ons it has.", true},
	{GrantActSend, "Send messages on its own", "A run that nobody watches may send messages beyond its delivery target.", true},
	{GrantActSpend, "Use paid or external tools on its own", "A run that nobody watches may use paid or external tools and the network.", true},
	{GrantSavedAgents, "Call your saved agents", "The agent can list your saved agents and start one as a sub-agent.", false},
	{GrantHistoryRead, "Read your earlier chats", "The agent can search and read your earlier chats. An agent that works in a folder reads only the chats of its own. Private chats, messaging chats and sub-agent runs are never read.", true},
}

// Grants returns the declared grants in display order. The caller may change
// the copy.
func Grants() []Grant { return slices.Clone(grants) }

// LookupGrant returns the declaration of a grant name.
func LookupGrant(name string) (Grant, bool) {
	for _, g := range grants {
		if g.Name == name {
			return g, true
		}
	}
	return Grant{}, false
}

// ValidGrant reports whether name is a declared grant.
func ValidGrant(name string) bool { _, ok := LookupGrant(name); return ok }

// SensitiveGrant reports whether name is a declared sensitive grant.
func SensitiveGrant(name string) bool { g, ok := LookupGrant(name); return ok && g.Sensitive }

// holdingOrigin says whether a run that started with this origin can hold the
// rights of its agent: a chat a person watches and a connector chat can. A
// run that started by itself (trigger, job) starts read only; what it may act
// on is the business of the action policy, not of a tool that exists.
func holdingOrigin(origin string) bool { return origin == "" || origin == session.OriginConnector }

// HoldsGrant is the tool-exists rule for a grant: the chat holds it when its run may
// have it at all (not a sub-agent, not a run that started by itself) and, for a
// chat of a saved agent, the owner granted it and the user may still use it
// (Grants is that intersection). A chat without an agent is the user's own and
// holds what the user may use; whether the server offers the feature is the
// tool's own check.
func (c Chat) HoldsGrant(grant string) bool {
	if c.SubAgent || !holdingOrigin(c.Origin) {
		return false
	}
	if !c.Agent {
		return true
	}
	return slices.Contains(c.Grants, grant)
}
