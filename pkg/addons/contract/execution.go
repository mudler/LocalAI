// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
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
	// Profiles are the environments the provider can run commands in. The
	// first one is the fallback and has the ID FallbackProfile. When there
	// are profiles, the host builds the note for the model from the profile
	// of the chat and Note is not used.
	Profiles []ExecutionProfile `json:"profiles,omitempty"`
}

// FallbackProfile is the ID of the profile a provider runs commands in when
// the profile a chat asked for is missing or cannot be prepared. It is the
// first profile of the descriptor.
const FallbackProfile = "base"

// Limits of the profiles of a provider.
const (
	MaxExecutionProfiles       = 8
	MaxProfileTitleChars       = 40
	MaxProfileDescriptionChars = 200
	MaxProfileTools            = 40
	MaxProfileToolNameChars    = 40
	MaxProfileToolDetailChars  = 120
	MaxProfileToolChecks       = 4
	MaxProfileCheckChars       = 80
	MaxProfileCPUs             = 64
	MaxProfileMemoryMiB        = 256 << 10
	MaxProfileDiskMiB          = 1 << 20
)

var (
	profileIDShape    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	profileCheckShape = regexp.MustCompile(`^[A-Za-z0-9._+-]+( -{1,2}[A-Za-z0-9-]+)*$`)
)

// ValidProfileID says whether id has the shape of a profile ID.
func ValidProfileID(id string) bool { return profileIDShape.MatchString(id) }

// ExecutionProfile is one environment of a commands provider: a name, what it
// is for, the limits of its sandboxes and the tools it has. It carries no
// authority and no image: the provider holds the image of each profile, and
// the host decides who may use the profile. A limit of zero means the
// provider's default.
type ExecutionProfile struct {
	// ID is a short lower case name, such as base or media.
	ID string `json:"id"`
	// Title is the name the person sees.
	Title string `json:"title"`
	// Description says what the profile is for, in one sentence.
	Description string `json:"description"`
	// CPUs, MemoryMiB and DiskMiB are the limits of one sandbox.
	CPUs      int `json:"cpus,omitempty"`
	MemoryMiB int `json:"memoryMiB,omitempty"`
	DiskMiB   int `json:"diskMiB,omitempty"`
	// Tools are the programs the environment has. The provider's image build
	// fails when one of them is missing (see ProfileTool.Check).
	Tools []ProfileTool `json:"tools"`
}

// ProfileTool is one tool of a profile.
type ProfileTool struct {
	// Name is how the model and the person read it, for example ffmpeg.
	Name string `json:"name"`
	// Detail is an optional short remark, for example the libraries of Python.
	Detail string `json:"detail,omitempty"`
	// Check are the commands the image build runs to prove the tool works,
	// each a program name with its version flags, for example "jq --version".
	Check []string `json:"check,omitempty"`
}

// Text is the tool as the model reads it: the name, and the detail in
// parentheses when there is one.
func (t ProfileTool) Text() string {
	if t.Detail == "" {
		return t.Name
	}
	return t.Name + " (" + t.Detail + ")"
}

// ToolList is the tools of the profile as one comma separated text.
func (p ExecutionProfile) ToolList() string {
	parts := make([]string, len(p.Tools))
	for i, t := range p.Tools {
		parts[i] = t.Text()
	}
	return strings.Join(parts, ", ")
}

// ProfileByID returns the profile with the given ID.
func (e *ExecutionDescriptor) ProfileByID(id string) (ExecutionProfile, bool) {
	if e != nil {
		for _, p := range e.Profiles {
			if p.ID == id {
				return p, true
			}
		}
	}
	return ExecutionProfile{}, false
}

func (p ExecutionProfile) validate(tool string) error {
	bad := func(what string) error { return fmt.Errorf("tool %s: profile %q: %s", tool, p.ID, what) }
	switch {
	case !ValidProfileID(p.ID):
		return bad("invalid ID")
	case !plainText(p.Title, MaxProfileTitleChars):
		return bad("invalid title")
	case !plainText(p.Description, MaxProfileDescriptionChars):
		return bad("invalid description")
	case p.CPUs < 0 || p.CPUs > MaxProfileCPUs, p.MemoryMiB < 0 || p.MemoryMiB > MaxProfileMemoryMiB, p.DiskMiB < 0 || p.DiskMiB > MaxProfileDiskMiB:
		return bad("a limit is out of range")
	case len(p.Tools) == 0 || len(p.Tools) > MaxProfileTools:
		return bad("a profile needs one to " + fmt.Sprint(MaxProfileTools) + " tools")
	}
	seen := map[string]bool{}
	for _, t := range p.Tools {
		switch {
		case !plainText(t.Name, MaxProfileToolNameChars), t.Detail != "" && !plainText(t.Detail, MaxProfileToolDetailChars):
			return bad("invalid tool text")
		case seen[t.Name]:
			return bad("repeated tool " + t.Name)
		case len(t.Check) > MaxProfileToolChecks:
			return bad("too many checks for tool " + t.Name)
		}
		seen[t.Name] = true
		for _, c := range t.Check {
			if len(c) > MaxProfileCheckChars || !profileCheckShape.MatchString(c) {
				return bad("invalid check for tool " + t.Name)
			}
		}
	}
	return nil
}

func (e *ExecutionDescriptor) validateProfiles(tool string) error {
	if len(e.Profiles) == 0 {
		return nil
	}
	if len(e.Profiles) > MaxExecutionProfiles {
		return fmt.Errorf("tool %s: too many profiles", tool)
	}
	if e.Profiles[0].ID != FallbackProfile {
		return fmt.Errorf("tool %s: the first profile must be %q, the fallback", tool, FallbackProfile)
	}
	seen := map[string]bool{}
	for _, p := range e.Profiles {
		if seen[p.ID] {
			return fmt.Errorf("tool %s: repeated profile %q", tool, p.ID)
		}
		seen[p.ID] = true
		if err := p.validate(tool); err != nil {
			return err
		}
	}
	return nil
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
	return e.validateProfiles(d.Name)
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
