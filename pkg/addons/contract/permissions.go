// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
)

// Limits of the permissions of a connector kind.
const (
	MaxConnectorRoles       = 8
	MaxConnectorPermissions = 12
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
	// CapToolsWrite lets a sender make an add-on tool change something outside
	// LocalAI, such as a comment on an issue. It is for the tools of a kind
	// whose binding holds a credential.
	CapToolsWrite = "tools.write"
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
	CapToolsWrite:       false,
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
	CapProposalsConfirm: {"Confirm changes from the chat", "A sender can confirm a change that the agent proposed, such as a memory or a trigger, by answering in the same chat."},
	CapMemoryWrite:      {"Save to memory", "The agent may remember what a sender asks it to remember."},
	CapFilesUpload:      {"Receive files", "The agent may read files that senders attach to a message."},
	CapTriggersPropose:  {"Propose triggers", "The agent may propose a timer or an event trigger when a sender asks for one."},
	CapTriggersRun:      {"Fire event triggers", "A message from a sender may start a run of an event trigger."},
	CapTalkbackChannels: {"Post to approved channels", "The agent may write to the approved channels below when a sender asks, besides the chat it answers in."},
	CapTalkbackNewChan:  {"Post to a new channel", "The agent may write to a channel that is not approved yet."},
	CapProfileWrite:     {"Change the agent profile", "The agent may change its own profile."},
	CapSkillsWrite:      {"Write skills", "The agent may write or change skills."},
	CapPagesWrite:       {"Write pages", "The agent may write or change pages."},
	CapDashboardWrite:   {"Write the dashboard", "The agent may change the dashboard."},
	CapToolsWrite:       {"Change things outside LocalAI", "An add-on tool may change something with the credential of the binding, such as a comment on an issue."},
}

// usualCapabilities are what an owner normally allows on a new binding: the
// everyday ones that stay inside LocalAI and the approved channels.
var usualCapabilities = map[string]bool{
	CapProposalsConfirm: true, CapMemoryWrite: true, CapFilesUpload: true,
	CapTriggersPropose: true, CapTriggersRun: true, CapTalkbackChannels: true,
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
// CapToolsWrite. credentialed says that the manifest declares credentials.
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
