// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
)

// Limits of the permissions of a connector kind.
const (
	MaxConnectorRoles       = 8
	MaxConnectorPermissions = 15
	// MaxMessageRoles is how many role IDs one ConnectorMessage carries.
	MaxMessageRoles = 8
)

// Capability names. The host owns this closed list. The same names are used
// for what an owner enables on a binding, what an owner grants to a role of a
// kind, and what a kind declares that it can grant.
const (
	CapProposalsConfirm = "proposals.confirm"
	CapMemoryWrite      = "memory.write"
	CapFilesUpload      = "files.upload"
	CapTriggersPropose  = "triggers.propose"
	CapTriggersRun      = "triggers.run"
	CapTalkbackChannels = "talkback.channels"
	CapTalkbackNewChan  = "talkback.new_channel"
	CapProfileWrite     = "profile.write"
	CapSkillsWrite      = "skills.write"
	CapPagesWrite       = "pages.write"
	CapDashboardWrite   = "dashboard.write"
	// CapKnowledgeWrite lets a sender have the agent propose a text for the
	// person's Knowledge. The owner still confirms each one.
	CapKnowledgeWrite = "knowledge.write"
	// CapToolsWrite lets a sender make an add-on tool change something, such
	// as a comment on an issue outside LocalAI, or a table of the owner. It is
	// for the tools of a kind whose binding holds a credential and for the
	// tools of an add-on whose resources are scoped per agent.
	CapToolsWrite = "tools.write"
	// CapLoopsWait lets the model of a connector chat wait for a change with
	// wait_for and wait_agents. A wait polls a source with code and costs no
	// model call until it ends, so it is not sensitive.
	CapLoopsWait = "loops.wait"
	// CapLoopsStart lets the model of a connector chat start a loop with
	// start_loop and end it with schedule_next and loop_done. Each run is a
	// model call that nobody asked for at that moment, so it is sensitive.
	CapLoopsStart = "loops.start"
)

// capabilities maps each name to whether it is sensitive. A sensitive
// capability is never granted to a role: only a paired sender can use it.
var capabilities = map[string]bool{
	CapProposalsConfirm: false,
	CapMemoryWrite:      false,
	CapFilesUpload:      false,
	CapTriggersPropose:  false,
	CapTriggersRun:      false,
	CapTalkbackChannels: false,
	CapTalkbackNewChan:  true,
	CapProfileWrite:     true,
	CapSkillsWrite:      true,
	CapPagesWrite:       true,
	CapDashboardWrite:   true,
	CapKnowledgeWrite:   true,
	CapToolsWrite:       false,
	CapLoopsWait:        false,
	CapLoopsStart:       true,
}

// KnownCapability reports whether name is in the closed list of capabilities.
func KnownCapability(name string) bool { _, ok := capabilities[name]; return ok }

// SensitiveCapability reports whether name is a sensitive capability.
func SensitiveCapability(name string) bool { return capabilities[name] }

// ConnectorCapabilityNames returns the closed list, sorted.
func ConnectorCapabilityNames() []string {
	out := make([]string, 0, len(capabilities))
	for n := range capabilities {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// CapabilityInfo describes one capability for a settings page: a plain title
// and a sentence that says what the owner allows. It adds no new rules.
type CapabilityInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive"`
	// Usual marks the capabilities that a settings page turns on with its
	// one-click preset. A sensitive capability is never usual.
	Usual bool `json:"usual"`
}

// capabilityText is the title and description of each capability. Titles are
// short verbs; descriptions say what changes and who can cause it.
var capabilityText = map[string][2]string{
	CapProposalsConfirm: {"Confirm changes from the chat", "A sender can confirm a trigger or a change that the agent proposed (a memory note, the agent rules, a skill, a page, the dashboard or a text for Knowledge) by answering yes or no in the same chat. Every change except a memory note needs a verified person."},
	CapMemoryWrite:      {"Save to memory", "The agent may remember what a sender asks it to remember."},
	CapFilesUpload:      {"Receive files", "The agent may read files that senders attach to a message."},
	CapTriggersPropose:  {"Propose triggers", "The agent may propose a timer or an event trigger when a sender asks for one."},
	CapTriggersRun:      {"Fire event triggers", "A message from a sender may start a run of an event trigger."},
	CapTalkbackChannels: {"Post to other channels", "The agent may write to the channels listed under Place the bot may post when a sender asks, besides the chat it answers in. This does not decide whose messages the bot trusts."},
	CapTalkbackNewChan:  {"Post to a new channel", "The agent may write to a place that is not on that list yet."},
	CapProfileWrite:     {"Change the agent profile", "The agent may change its own profile."},
	CapSkillsWrite:      {"Write skills", "The agent may write or change skills."},
	CapPagesWrite:       {"Write pages", "The agent may write or change pages."},
	CapDashboardWrite:   {"Write the dashboard", "The agent may change the dashboard."},
	CapKnowledgeWrite:   {"Add to knowledge", "The agent may propose a text for a collection of the Knowledge. The owner confirms each one."},
	CapToolsWrite:       {"Let people who are not verified change things with add-on tools", "An add-on tool may change something outside LocalAI or in your own data. Verified people already use the write tools of the add-ons the agent is connected to; the write mode of the connection still applies. Turn this on to let listed people who are not verified use them too, and to let verified people use the write tools of add-ons that have no connection. People let in only through a channel, or by a bot that accepts anyone, do not get them, unless a role grants it. The one exception is an add-on that keeps your own data, such as Calendar or Contacts: with this on, everyone the bot lets in can change that data."},
	CapLoopsWait:        {"Let the agent wait for a change", "The agent may wait for a source to change, or for its sub-agents to finish, and then answer in the same chat. A wait is checked by code and costs no model call until it ends."},
	CapLoopsStart:       {"Let the agent run on a timer", "The agent may start a loop in the chat: it comes back by itself, as often as the server limits allow, and each run is a model call. Only a verified person can use it. A loop started this way ends sooner than the same loop in your own chat. It is off until you turn it on."},
}

// usualCapabilities are what an owner normally allows on a new binding: the
// everyday ones that stay inside LocalAI and the channels it may post to.
var usualCapabilities = map[string]bool{
	CapProposalsConfirm: true, CapMemoryWrite: true, CapFilesUpload: true,
	CapTriggersPropose: true, CapTriggersRun: true, CapTalkbackChannels: true, CapLoopsWait: true,
}

// UsualConnectorCapabilities returns what the "Allow the usual" preset of a
// settings page turns on, sorted: the capabilities marked usual, never a
// sensitive one. A migration that applies the preset uses this list so it
// grants exactly what the button grants.
func UsualConnectorCapabilities() []string {
	var out []string
	for _, n := range ConnectorCapabilityNames() {
		if usualCapabilities[n] && !capabilities[n] {
			out = append(out, n)
		}
	}
	return out
}

// ConnectorCapabilities returns the closed list with its texts, in the order of
// ConnectorCapabilityNames.
func ConnectorCapabilities() []CapabilityInfo {
	names := ConnectorCapabilityNames()
	out := make([]CapabilityInfo, 0, len(names))
	for _, n := range names {
		text := capabilityText[n]
		out = append(out, CapabilityInfo{Name: n, Title: text[0], Description: text[1], Sensitive: capabilities[n], Usual: usualCapabilities[n] && !capabilities[n]})
	}
	return out
}

// ToolPermission is the capability that a sender needs to use a tool of an
// add-on in a connector chat, or "" when the tool needs none beyond the
// binding. The rule is by shape, so the host names no add-on: a tool that
// writes, in an add-on that holds a credential for the person's binding,
// changes something outside LocalAI with that person's token, and needs
// CapToolsWrite. So does a tool that writes in an add-on whose resources are
// scoped per agent, or in an add-on that declares ownerData: it changes the
// owner's data on a sender's word, so a sender needs the capability, which
// the owner and paired senders hold.
// credentialed says that the manifest declares credentials or per-agent
// resources.
func ToolPermission(credentialed bool, d ToolDefinition) string {
	if credentialed && d.Kind != ToolKindRead {
		return CapToolsWrite
	}
	return ""
}

// ConnectorRole is a role that a kind can give to a sender, such as the admin
// of a workspace. The add-on decides who has which role and sends the role IDs
// with each message; the owner decides what each role may do.
type ConnectorRole struct {
	// ID names the role in ConnectorMessage.Roles. It has the shape of an
	// add-on namespace ID.
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// Role returns the role of the kind with the given ID.
func (k ConnectorKind) Role(id string) (ConnectorRole, bool) {
	for _, r := range k.Roles {
		if r.ID == id {
			return r, true
		}
	}
	return ConnectorRole{}, false
}

// Grantable reports whether the kind lists the capability and the capability
// may go to a role: it is known and not sensitive.
func (k ConnectorKind) Grantable(capability string) bool {
	return KnownCapability(capability) && !SensitiveCapability(capability) && slices.Contains(k.Permissions, capability)
}

// FilterRoles keeps the role IDs of a message that the kind declares, without
// repeats and at most MaxMessageRoles. A kind that declares no roles keeps none.
func (k ConnectorKind) FilterRoles(ids []string) []string {
	var out []string
	for _, id := range ids {
		if len(out) == MaxMessageRoles {
			break
		}
		if _, ok := k.Role(id); ok && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func validatePermissions(k ConnectorKind) error {
	if len(k.Roles) == 0 && len(k.Permissions) == 0 {
		return nil
	}
	if len(k.Roles) > MaxConnectorRoles {
		return fmt.Errorf("a connector kind has at most %d roles", MaxConnectorRoles)
	}
	seen := map[string]bool{}
	for _, r := range k.Roles {
		if !namespace.MatchString(r.ID) || seen[r.ID] || !plainText(r.Title, MaxTitleChars) || (r.Description != "" && !plainText(r.Description, MaxDescriptionChars)) {
			return fmt.Errorf("invalid or repeated connector role")
		}
		seen[r.ID] = true
	}
	if len(k.Permissions) > MaxConnectorPermissions {
		return fmt.Errorf("a connector kind lists at most %d permissions", MaxConnectorPermissions)
	}
	listed := map[string]bool{}
	for _, p := range k.Permissions {
		if !KnownCapability(p) {
			return fmt.Errorf("unknown connector permission %q", p)
		}
		if listed[p] {
			return fmt.Errorf("repeated connector permission %q", p)
		}
		listed[p] = true
	}
	return nil
}
