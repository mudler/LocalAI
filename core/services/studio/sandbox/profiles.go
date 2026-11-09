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
// the host puts the CallOptions (see contract.ExecutionCallArgKey).
const CallArgKey = contract.ExecutionCallArgKey

// CallOptions is what the host decides for one call of a sandbox tool: the
// profile of the chat, the pin to the previous image and the caps on memory
// and disk (see contract.ExecutionCall).
type CallOptions = contract.ExecutionCall

// MissingToolNote is the line a call gets when a command was not found (exit
// code 127): it names the profile the sandbox runs and the profiles that have
// more programs, so the first missing tool gives a clear error instead of a
// bare "command not found". It never switches the profile: the model tells the
// user and may offer the switch.
func MissingToolNote(active string, all []contract.ExecutionProfile) string {
	var more []string
	for _, o := range all {
		if o.ID == active {
			continue
		}
		cur, _ := profileByID(all, active)
		have := map[string]bool{}
		for _, t := range cur.Tools {
			have[t.Name] = true
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
	note := "[A program was not found in the " + active + " sandbox profile."
	if len(more) > 0 {
		note += " Profiles with more programs: " + strings.Join(more, "; ") + "."
	}
	return note + " Tell the user, and offer sandbox_profile (action use) to switch. Do not try to install it.]"
}

func profileByID(all []contract.ExecutionProfile, id string) (contract.ExecutionProfile, bool) {
	for _, p := range all {
		if p.ID == id {
			return p, true
		}
	}
	return contract.ExecutionProfile{}, false
}
