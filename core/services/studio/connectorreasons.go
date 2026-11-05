package studio

import (
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// This file is the one table of the reasons a connector capability is refused.
// Authorize and the capability switches produce a code where they decide; the
// owner's settings page, the note the model reads and the tool errors all say
// the reason from this table. Nothing here decides: adding a code does not
// change who may do what.

// The codes of a refusal. The set is closed (see TestBlockCodesAreClosed): a
// client maps each code to its own copy and may rely on the list.
const (
	// CodeNoAgent: the connection has no agent, it holds credentials only.
	CodeNoAgent = "no_agent"
	// CodeUnknownCapability: the name is not on the closed list.
	CodeUnknownCapability = "unknown_capability"
	// CodeServerSettingOff: an administrator turned the feature off for the
	// whole server.
	CodeServerSettingOff = "server_setting_off"
	// CodeToolsSwitchOff: the tools group that serves the capability is
	// switched off for the agent.
	CodeToolsSwitchOff = "tools_switch_off"
	// CodeAgentPermissionOff: the agent was not granted the permission.
	CodeAgentPermissionOff = "agent_permission_off"
	// CodeAgentUnavailable: the agent of the connection cannot be read.
	CodeAgentUnavailable = "agent_unavailable"
	// CodeConnectionPermissionOff: the owner did not tick the capability on
	// the connection.
	CodeConnectionPermissionOff = "connection_permission_off"
	// CodeSenderNotListed: the sender is not let in at all.
	CodeSenderNotListed = "sender_not_listed"
	// CodeSenderNeedsPairing: the capability is sensitive and the sender is
	// not paired.
	CodeSenderNeedsPairing = "sender_needs_pairing"
	// CodeChannelNotTrusted: the sender is let in through a channel or because
	// the connection accepts anyone, so they are not trusted by name.
	CodeChannelNotTrusted = "channel_not_trusted"
	// CodeRoleNotGranted: the sender has a role that holds no grant for it.
	CodeRoleNotGranted = "role_not_granted"
	// CodeKindNotRunning: the add-on of the connection does not run, so roles
	// cannot be known.
	CodeKindNotRunning = "kind_not_running"
	// CodeContextRule: the capability is held but this place or thread is not
	// one the feature allows.
	CodeContextRule = "context_rule"
	// CodeKnowledgeNone: the agent has no collection on its list, so there is
	// nowhere to add a text.
	CodeKnowledgeNone = "knowledge_none"
	// CodeKnowledgeNoEmbedding: the model that indexes Knowledge is not
	// installed on the server.
	CodeKnowledgeNoEmbedding = "knowledge_no_embedding"
	// CodeAgentResourcesNone: every add-on of the agent that changes things
	// works on resources the agent lists, and the list is empty, so there is
	// nothing the bot may change.
	CodeAgentResourcesNone = "agent_resources_none"
	// CodeAgentResourcesReadOnly: the agent lists resources, but only to read.
	CodeAgentResourcesReadOnly = "agent_resources_read_only"
	// CodeDuplicateConnection: another enabled connection of the owner is the
	// same bot, so both answer the same message. It is not a refusal of a
	// capability: the sentence names the other connection with {other}, and
	// DuplicateMessage fills it.
	CodeDuplicateConnection = "duplicate_connection"
)

// BlockCodes lists every code, in the order of the chain.
func BlockCodes() []string {
	return []string{CodeNoAgent, CodeUnknownCapability, CodeServerSettingOff, CodeToolsSwitchOff, CodeAgentPermissionOff,
		CodeAgentUnavailable, CodeConnectionPermissionOff, CodeSenderNotListed, CodeSenderNeedsPairing,
		CodeChannelNotTrusted, CodeRoleNotGranted, CodeKindNotRunning, CodeContextRule,
		CodeKnowledgeNone, CodeKnowledgeNoEmbedding, CodeAgentResourcesNone, CodeAgentResourcesReadOnly, CodeDuplicateConnection}
}

// Who holds the link of the chain that refused.
const (
	ByServer     = "server"
	ByTools      = "tools"
	ByAgent      = "agent"
	ByConnection = "connection"
	BySender     = "sender"
	// ByApproval is not a blocker. It names the last link of the chain of a
	// trigger, which shapes the result (ask, or at once) and so is listed in
	// ThenCheck for the capability that proposes one.
	ByApproval = "approval"
)

// chain is the order in which the links are checked: the first one that fails
// is the one reported.
var chain = []string{ByServer, ByTools, ByAgent, ByConnection, BySender}

// The kinds of fix. A client turns each into a link of its own; the server
// never puts a URL in the answer.
const (
	FixOpenServerSetting = "open_server_setting"
	FixOpenAgent         = "open_agent"
	FixOpenConnection    = "open_connection"
	FixPairSender        = "pair_sender"
	FixAddPerson         = "add_person"
)

// Fix points at the one place that can solve a refusal.
type Fix struct {
	Kind string `json:"kind"`
	// Target is a stable ID: a server setting key, an agent ID, a field of
	// the connection form ("caps", "grants", "agent_id", "allow") or
	// "pairing".
	Target string `json:"target"`
	// Field narrows the target where it helps: the capability to tick on the
	// connection, or the permission to grant on the agent.
	Field string `json:"field,omitempty"`
}

// Block is the verdict of a capability switch: zero when the capability is
// on. Reason is the English sentence for logs and tests; Code says which link
// refused.
type Block struct {
	Code   string
	Reason string
	Fix    *Fix
}

// Off says whether the switch refuses.
func (b Block) Off() bool { return b.Code != "" || b.Reason != "" }

// blockInfo is a row of the table: who holds the link, what the owner reads,
// and what the person who asked (and the model) is told. Both texts are whole
// sentences. {action} stands for the verb phrase of the capability ("set
// reminders"), so a template reads "the bot cannot {action}" and never needs a
// noun where a verb belongs.
type blockInfo struct {
	By string
	// Message is for the owner, on the connection page.
	Message string
	// Say is for the sender and the model. It names no setting, host or
	// error code, and goes to the person who asked, never to a channel.
	Say string
}

var blockTable = map[string]blockInfo{
	CodeNoAgent: {ByConnection,
		"This connection has no agent, so the bot cannot answer or act yet.",
		"This bot has no agent set up yet."},
	CodeUnknownCapability: {ByServer,
		"This server does not know that permission.",
		"That is not something this bot can be allowed to do."},
	CodeServerSettingOff: {ByServer,
		"An administrator turned this off for the whole server, so the bot cannot {action}.",
		"An administrator turned this off on the server, so the bot cannot {action}."},
	CodeToolsSwitchOff: {ByTools,
		"The tools the bot needs to {action} are switched off for the agent. Switch them on in the agent.",
		"The owner switched off the tools the bot needs to {action}."},
	CodeAgentPermissionOff: {ByAgent,
		"The agent is not allowed to {action}. Turn on the matching permission in the agent.",
		"The owner has not allowed the agent to {action}."},
	CodeAgentUnavailable: {ByAgent,
		"The agent of this connection cannot be read right now. Check that it still exists.",
		"The agent of this bot cannot be read right now."},
	CodeConnectionPermissionOff: {ByConnection,
		"This connection does not allow the bot to {action}. Tick the matching permission on the connection.",
		"The owner has not allowed this bot to {action}."},
	CodeSenderNotListed: {BySender,
		"This person is not on the list of people the bot answers. Add them to the allowed people.",
		"You are not on the list of people this bot answers."},
	CodeSenderNeedsPairing: {BySender,
		"This person needs to be paired with this bot before the bot can {action} for them.",
		"You need to be paired with this bot by the owner before it can {action} for you."},
	CodeChannelNotTrusted: {BySender,
		"This channel is set to Answer only, so the bot cannot {action} for its members. Add the person, or set it to Let everyone here ask for actions.",
		"This bot can {action} only for people the owner added by name or paired, and you are not one of them."},
	CodeRoleNotGranted: {BySender,
		"The role of this person does not let the bot {action}. Allow it for that role on the connection.",
		"The owner has not allowed this bot to {action} for your role."},
	CodeKindNotRunning: {BySender,
		"The app for this connection is not running, so roles cannot be checked. Start the app.",
		"The app for this bot is not running, so it cannot check what you may do."},
	CodeKnowledgeNone: {ByAgent,
		"No collection is on the list of the agent, so the bot has nowhere to {action}. Add a collection to the agent.",
		"This bot has no collection to {action} yet."},
	CodeKnowledgeNoEmbedding: {ByServer,
		"The embedding model for Knowledge is not installed, so the bot cannot {action}. Install it in Models.",
		"The server cannot index new texts right now, so the bot cannot {action}."},
	CodeAgentResourcesNone: {ByAgent,
		"No resource is on the list of the agent, so the bot cannot {action}. Add one to the agent.",
		"The owner gave this bot no resource to work on, so it cannot {action}."},
	CodeAgentResourcesReadOnly: {ByAgent,
		"Every resource on the list of the agent is read only, so the bot cannot {action}. Allow changes for one of them in the agent.",
		"The owner let this bot read what it works on, not change it, so it cannot {action}."},
	CodeDuplicateConnection: {ByConnection,
		"This bot is also connected as {other}. Both connections will answer the same message.",
		"This bot is connected twice, so a second copy may answer too."},
	CodeContextRule: {ByConnection,
		"The bot cannot {action} in this place.",
		"This bot cannot {action} in this place."},
}

// capabilityPhrase says a capability in the words a person uses. Action is a
// verb phrase in the infinitive and fills "the bot cannot ...". Noun names the
// same thing as a noun phrase, for the places that cannot rewrite a sentence
// (see deniedToolsNote). The reasons of Authorize name the capability; the
// model and the owner's page should not repeat a key that the person never saw.
type capabilityPhrase struct{ Action, Noun string }

var capabilityWords = map[string]capabilityPhrase{
	contract.CapProposalsConfirm: {"confirm proposals", "confirming proposals"},
	contract.CapMemoryWrite:      {"save notes to memory", "saving to memory"},
	contract.CapFilesUpload:      {"receive files", "receiving files"},
	contract.CapTriggersPropose:  {"set reminders and scheduled tasks", "reminders and scheduled tasks"},
	contract.CapTriggersRun:      {"run scheduled tasks", "running scheduled tasks"},
	contract.CapTalkbackChannels: {"write to other channels", "writing to other channels"},
	contract.CapTalkbackNewChan:  {"start conversations in new channels", "starting conversations in new channels"},
	contract.CapProfileWrite:     {"change the agent's rules", "changing the agent's rules"},
	contract.CapSkillsWrite:      {"save skills", "saving skills"},
	contract.CapPagesWrite:       {"publish pages", "publishing pages"},
	contract.CapDashboardWrite:   {"change the dashboard", "changing the dashboard"},
	contract.CapKnowledgeWrite:   {"write to knowledge", "writing to knowledge"},
	contract.CapToolsWrite:       {"change things in other services with add-on tools", "changes made by add-on tools"},
}

// actionOf is the verb phrase of a capability, "do this" for a name that is
// not on the list.
func actionOf(capability string) string {
	// The read rule of the personal notes is not a capability of the closed
	// list, but it is refused with the same texts.
	if capability == NotesReadCapability {
		return "read the owner's personal notes"
	}
	if w, ok := capabilityWords[capability]; ok {
		return w.Action
	}
	return "do this"
}

// blockMessage is what the owner reads for a code.
func blockMessage(code, capability string) string {
	row, ok := blockTable[code]
	if !ok {
		return ""
	}
	return strings.ReplaceAll(row.Message, "{action}", actionOf(capability))
}

// BlockSay is what the person who asked, and the model, is told for a code:
// one short plain sentence with no setting names.
func BlockSay(code, capability string) string {
	row, ok := blockTable[code]
	if !ok {
		return ""
	}
	return strings.ReplaceAll(row.Say, "{action}", actionOf(capability))
}

// Say is the sentence for the person who asked and for the model. A refusal
// with no code (a rule of the feature, written by it) keeps its own reason.
func (d Decision) Say() string {
	if s := BlockSay(d.Code, d.Capability); s != "" {
		return s
	}
	return d.Reason
}

// thenCheck lists the links of the chain after the one that refused, so the
// owner knows more may follow. The approval mode follows for a trigger.
func thenCheck(by, capability string) []string {
	i := slices.Index(chain, by)
	if i < 0 {
		return nil
	}
	out := slices.Clone(chain[i+1:])
	if capability == contract.CapTriggersPropose {
		out = append(out, ByApproval)
	}
	return out
}

// chainRank orders the links so that a refusal from a switch is compared with
// the others by where it stands in the chain, not by when it was registered.
func chainRank(code string) int {
	row, ok := blockTable[code]
	if !ok {
		return slices.Index(chain, ByServer)
	}
	return slices.Index(chain, row.By)
}

// BlockOwnerMessage is the English sentence the owner reads for a code: the
// source of the translations that connectortext holds by the same code.
func BlockOwnerMessage(code, capability string) string { return blockMessage(code, capability) }
