package sandbox

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// NoInstallNote ends every note of the tools: the guest user cannot install
// packages, so a program that is not listed is not there.
const NoInstallNote = "apt cannot install anything."

// ProfileNote is the sentence of the Commands block of the prompt that tells
// the model which profile its sandbox runs and which programs that profile
// has. The other profiles are named with the programs they add, so the model
// can offer the switch instead of failing. It is built from the profile the
// add-on declares, never from a list in the host.
func ProfileNote(p contract.ExecutionProfile, all []contract.ExecutionProfile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The sandbox profile is %s. It has: %s.", p.ID, p.ToolList())
	have := map[string]bool{}
	for _, t := range p.Tools {
		have[t.Name] = true
	}
	var more []string
	for _, o := range all {
		if o.ID == p.ID {
			continue
		}
		var extra []string
		for _, t := range o.Tools {
			if !have[t.Name] {
				extra = append(extra, t.Name)
			}
		}
		if len(extra) > 0 {
			more = append(more, fmt.Sprintf("%s (adds %s)", o.ID, strings.Join(extra, ", ")))
		}
	}
	if len(more) > 0 {
		b.WriteString(" Other profiles: " + strings.Join(more, "; ") + ".")
	}
	b.WriteString(" Anything else is not installed, and " + NoInstallNote)
	return b.String()
}

// ProfileToolsSentence is the short form for a tool description: the profile
// and its programs, without the other profiles.
func ProfileToolsSentence(p contract.ExecutionProfile) string {
	return fmt.Sprintf("Sandbox profile %s: %s.", p.ID, p.ToolList())
}

// CallArgKey is the key of the arguments of a sandbox tool call under which
// the host puts the CallOptions. The model cannot set it: the schema of the
// tool has no such property, and the host overwrites it.
const CallArgKey = "sandbox"

// CallOptions is what the host decides for one call of a sandbox tool: the
// profile of the chat, the administrator's pin to the previous image and the
// caps on memory and disk. The add-on trusts it as it trusts the user and the
// chat the host names. A zero value asks for the add-on's defaults.
type CallOptions struct {
	// Profile is the ID of the profile the chat runs. Empty: the fallback.
	Profile string `json:"profile,omitempty"`
	// Pin asks for the previous image of the profile, when there is one.
	Pin bool `json:"pin,omitempty"`
	// MemoryMiB and DiskMiB cap the sandbox. Zero means no cap.
	MemoryMiB int `json:"memoryMiB,omitempty"`
	DiskMiB   int `json:"diskMiB,omitempty"`
}
