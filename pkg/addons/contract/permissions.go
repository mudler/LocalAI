// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
)

// MinPermissionsAPI is the first manifest API version that knows the roles and
// permissions of a connector kind.
const MinPermissionsAPI uint64 = 2

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

func validatePermissions(k ConnectorKind, api VersionRange) error {
	if len(k.Roles) == 0 && len(k.Permissions) == 0 {
		return nil
	}
	if api.Max < MinPermissionsAPI {
		return fmt.Errorf("connector roles and permissions need api %d", MinPermissionsAPI)
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
