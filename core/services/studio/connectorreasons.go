package studio

import (
	"fmt"
	"slices"

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
)

// BlockCodes lists every code, in the order of the chain.
func BlockCodes() []string {
	return []string{CodeNoAgent, CodeUnknownCapability, CodeServerSettingOff, CodeToolsSwitchOff, CodeAgentPermissionOff,
		CodeAgentUnavailable, CodeConnectionPermissionOff, CodeSenderNotListed, CodeSenderNeedsPairing,
		CodeChannelNotTrusted, CodeRoleNotGranted, CodeKindNotRunning, CodeContextRule}
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
// and what the person who asked (and the model) is told.
type blockInfo struct {
	By string
	// Message is for the owner. %s is the capability in words.
	Message string
	// Say is for the sender and the model. It names no setting, host or
	// error code, and goes to the person who asked, never to a channel.
	Say string
}

var blockTable = map[string]blockInfo{
	CodeNoAgent:                 {ByConnection, "This connection has no agent.", "this bot has no agent"},
	CodeUnknownCapability:       {ByServer, "That is not a permission this server knows.", "that is not something this bot can be allowed to do"},
	CodeServerSettingOff:        {ByServer, "An administrator turned %s off for this whole server.", "%s is turned off on this server; an administrator can change that"},
	CodeToolsSwitchOff:          {ByTools, "The tools for %s are switched off for the agent.", "the owner has switched off the tools for %s"},
	CodeAgentPermissionOff:      {ByAgent, "The agent has not been given permission for %s.", "the owner has not given the agent permission for %s"},
	CodeAgentUnavailable:        {ByAgent, "The agent of this connection cannot be read.", "the agent of this bot cannot be read"},
	CodeConnectionPermissionOff: {ByConnection, "This connection does not allow %s.", "the owner has not allowed %s for this bot"},
	CodeSenderNotListed:         {BySender, "This person is not on the list of people the bot answers.", "you are not on the list of people this bot answers"},
	CodeSenderNeedsPairing:      {BySender, "This person has to be paired with the bot by the owner before %s.", "%s needs you to be paired with this bot by the owner"},
	CodeChannelNotTrusted:       {BySender, "This person is let in through a channel, or because the bot accepts anyone, so they are not trusted for %s. Add them by name or pair them.", "%s is only for people the owner added by name or paired"},
	CodeRoleNotGranted:          {BySender, "The role of this person was not given %s.", "the owner has not allowed %s for your role"},
	CodeKindNotRunning:          {BySender, "The app for this connection is not running, so roles cannot be checked.", "the app for this bot is not running"},
	CodeContextRule:             {ByConnection, "%s is not allowed in this place.", "%s is not allowed in this place"},
}

// capabilityWords says a capability in the words a person uses. The reasons of
// Authorize name the capability; the model and the owner's page should not
// repeat a key that the person never saw.
var capabilityWords = map[string]string{
	contract.CapProposalsConfirm: "confirming proposals",
	contract.CapMemoryWrite:      "saving to memory",
	contract.CapFilesUpload:      "receiving files",
	contract.CapTriggersPropose:  "reminders and scheduled tasks",
	contract.CapTriggersRun:      "running scheduled tasks",
	contract.CapTalkbackChannels: "writing to other channels",
	contract.CapTalkbackNewChan:  "starting conversations in new channels",
	contract.CapProfileWrite:     "changing the agent's rules",
	contract.CapSkillsWrite:      "saving skills",
	contract.CapPagesWrite:       "publishing pages",
	contract.CapDashboardWrite:   "changing the dashboard",
	contract.CapToolsWrite:       "changes made by add-on tools",
}

func wordsOf(capability string) string {
	if w, ok := capabilityWords[capability]; ok {
		return w
	}
	return "this"
}

// blockMessage is what the owner reads for a code.
func blockMessage(code, capability string) string {
	row, ok := blockTable[code]
	if !ok {
		return ""
	}
	return sprintfOnce(row.Message, wordsOf(capability))
}

// BlockSay is what the person who asked, and the model, is told for a code:
// one short plain sentence with no setting names.
func BlockSay(code, capability string) string {
	row, ok := blockTable[code]
	if !ok {
		return ""
	}
	return sprintfOnce(row.Say, wordsOf(capability))
}

func sprintfOnce(format, arg string) string {
	for i := 0; i+1 < len(format); i++ {
		if format[i] == '%' && format[i+1] == 's' {
			return fmt.Sprintf(format, arg)
		}
	}
	return format
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
