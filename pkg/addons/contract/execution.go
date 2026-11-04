// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
)

// ExecutionClassCommands is the class of a tool that runs shell commands and
// code: the tool that shadows bash.
const ExecutionClassCommands = "commands"

// Kinds of skills a commands provider can supply the runtime for.
const (
	// SkillKindDocuments are the skills of the document slots (see
	// DocumentSkill). They need a provider that reports isolated execution.
	SkillKindDocuments = "documents"
)

// Limits of the text the host shows for an execution provider.
const (
	MaxExecutionTitleChars = 40
	MaxExecutionNoteChars  = 600
)

// ExecutionDescriptor is what an add-on declares about the tool that runs
// commands. It carries no authority. Whether commands are ready and isolated
// is what the provider reports at run time (ExecutionStatus); the host decides
// from that report and never from the add-on's ID.
type ExecutionDescriptor struct {
	// Class is ExecutionClassCommands.
	Class string `json:"class"`
	// Title names the provider in the text the host shows the person and the
	// model, for example "Sandbox". It falls back to the add-on's title.
	Title string `json:"title,omitempty"`
	// Note is one plain sentence for the model when the provider reports
	// isolated execution, such as the programs its environment has.
	Note string `json:"note,omitempty"`
	// Skills says which skills run on this provider.
	Skills *ExecutionSkills `json:"skills,omitempty"`
}

// ExecutionSkills declares the skills whose runtime the provider supplies.
type ExecutionSkills struct {
	// ProvidesRuntimeFor lists skill kinds (SkillKindDocuments). The host
	// offers the add-on's skills of those kinds only while the provider
	// reports isolated execution.
	ProvidesRuntimeFor []string `json:"providesRuntimeFor,omitempty"`
}

// validate checks the descriptor of tool d. A nil descriptor is valid.
func (e *ExecutionDescriptor) validate(d ToolDefinition) error {
	if e == nil {
		return nil
	}
	if e.Class != ExecutionClassCommands || d.Shadows != "bash" {
		return fmt.Errorf("tool %s: an execution descriptor has class %q and belongs to a tool that shadows bash", d.Name, ExecutionClassCommands)
	}
	if e.Title != "" && !plainText(e.Title, MaxExecutionTitleChars) {
		return fmt.Errorf("tool %s: invalid execution title", d.Name)
	}
	if e.Note != "" && !plainText(e.Note, MaxExecutionNoteChars) {
		return fmt.Errorf("tool %s: invalid execution note", d.Name)
	}
	if e.Skills != nil {
		for _, k := range e.Skills.ProvidesRuntimeFor {
			if k != SkillKindDocuments {
				return fmt.Errorf("tool %s: unknown skill kind %q", d.Name, k)
			}
		}
	}
	return nil
}

// CommandsTool returns the tool of the definitions that shadows bash.
func CommandsTool(defs []ToolDefinition) (ToolDefinition, bool) {
	i := slices.IndexFunc(defs, func(d ToolDefinition) bool { return d.Shadows == "bash" && ValidateShadow(d) == nil })
	if i < 0 {
		return ToolDefinition{}, false
	}
	return defs[i], true
}

// IsolatedSkills lists the skills of an add-on that need its own commands
// provider to report isolated execution: the document-slot skills of an
// add-on with a commands tool, unless its descriptor says it does not provide
// the runtime for documents. A commands tool without a descriptor counts as providing it, so such an add-on keeps the check.
func IsolatedSkills(defs []ToolDefinition, skills []SkillDefinition) []string {
	tool, ok := CommandsTool(defs)
	if !ok {
		return nil
	}
	if e := tool.Execution; e != nil && (e.Skills == nil || !slices.Contains(e.Skills.ProvidesRuntimeFor, SkillKindDocuments)) {
		return nil
	}
	var out []string
	for _, s := range SlotsOf(skills) {
		if s.Kind == SlotKindDocuments {
			out = append(out, s.Name)
		}
	}
	return out
}
