package enginesettings

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Capability names an optional feature of an engine. A client shows the
// controls of a capability only when the engine lists it.
type Capability string

const (
	// CapabilityCommandLine: the engine reads a pasted command line
	// (ParseCommand) and writes the command of a config (BuildCommand).
	CapabilityCommandLine Capability = "command-line"
	// CapabilityTensorPlacement: the engine builds and recognises an options
	// entry that keeps MoE expert tensors on the CPU.
	CapabilityTensorPlacement Capability = "tensor-placement"
)

// ErrUnsupported is returned when an engine lacks the capability an
// operation needs.
var ErrUnsupported = errors.New("the engine does not support this operation")

// Engine describes the runtime settings of one inference engine.
type Engine struct {
	// ID is a stable identifier, for example "llama-cpp". It is the path
	// segment of the engine's API routes.
	ID string
	// Name is the display name, for example "llama.cpp".
	Name string
	// Backends says which model backend names this engine serves.
	Backends BackendMatch
	// Groups lists the editor sections, in the order they are drawn.
	Groups []Group
	// Flags is the table of settings, in the order of a built command and
	// of the known entries of "options".
	Flags []Flag
	// Combined lists controls that write several flags at once.
	Combined []Combined
	// Presets lists whole-setup starting points. Optional.
	Presets []Preset
	// ReservedOptions are option names another part of LocalAI writes. The
	// editor does not offer them as raw options.
	ReservedOptions []string
	// Command adds the command-line capability. Optional.
	Command *CommandLine
	// Placement adds the tensor-placement capability. Optional.
	Placement *TensorPlacement
}

// CommandLine describes how the engine's command line maps to a model config.
type CommandLine struct {
	// Binary is the first word of a built command, for example "llama-server".
	Binary string
	// Example is a short command shown as a hint where a command is pasted.
	Example string
	// IsBinary says whether the first word of a pasted command is the
	// program name. Nil compares the base name with Binary.
	IsBinary func(word string) bool
	// Ignored lists flags LocalAI sets itself or that would stop the server.
	// They are reported and never forwarded.
	Ignored []IgnoredFlag
	// Special lists flags outside the table, with their own handlers.
	Special []SpecialFlag
	// Tokenize splits a command line into words. Nil uses Tokenize.
	Tokenize func(string) (tokens, operators []string, err error)
}

// IgnoredFlag is a flag a pasted command may have that LocalAI never forwards.
type IgnoredFlag struct {
	CLI    []string
	Reason string
	// NoValue marks a flag that takes no value.
	NoValue bool
}

// SpecialFlag is a flag that does not map to one config key, for example a
// flag that sets several keys at once.
type SpecialFlag struct {
	CLI []string
	// Paths maps the config paths Build reads to their type, so a request
	// with a value of the wrong type is refused before Build runs.
	Paths map[string]ValueType
	// Covers lists the table keys this flag writes in a built command. It
	// is written at the place of the first of them, and those keys are not
	// written by the table. Empty: written right after the binary.
	Covers []string
	// Parse stores the flag's value. ok is false when the flag has no value.
	Parse func(p *ParseState, flag, value string, ok bool)
	// Build returns the words of the flag for a config, or nil.
	Build func(b *BuildState) []string
}

// Combined is a control that writes several flags at once, such as a load
// mode that sets mmap, mlock and direct I/O.
type Combined struct {
	ID string `json:"id"`
	// Flag is the command-line flag that sets the same values, if any.
	Flag  string `json:"flag,omitempty"`
	Group Group  `json:"group"`
	Doc   string `json:"doc"`
	// Keys are the flag keys the control writes.
	Keys []string `json:"keys"`
	// Unset is the value that applies to a key that is not set.
	Unset map[string]any `json:"unset"`
	// Choices are the values of the control. A choice sets each key to a
	// value, or unsets it with null. To read the current choice, a client
	// takes the choice that sets nothing when every key is unset; else it
	// tries the choices from the last to the first and takes the first one
	// whose non-null values all equal the values in the config (Unset for a
	// missing key).
	Choices []Choice `json:"choices"`
}

// Choice is one value of a Combined control.
type Choice struct {
	Value string         `json:"value"`
	Label string         `json:"label"`
	Set   map[string]any `json:"set"`
}

// Preset is a whole-setup starting point.
type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Settings maps flag keys to values.
	Settings map[string]any `json:"settings"`
	// Placement is an optional tensor placement. Only for engines with the
	// tensor-placement capability.
	Placement *Placement `json:"placement,omitempty"`
}

// TensorPlacement is the tensor-placement capability.
type TensorPlacement struct {
	// Option is the options name of a placement entry, for example "-ot".
	Option string
	// Group is the editor section the control is drawn in.
	Group Group
	// Modes lists the placement modes the engine builds.
	Modes []PlacementMode
	// Build returns the options entry of a placement.
	Build func(Placement) (string, error)
	// Recognise reads an options entry back. False: the engine did not
	// build it, and a client shows it as raw text.
	Recognise func(entry string) (Placement, bool)
}

// Capabilities lists the optional features of the engine.
func (e *Engine) Capabilities() []Capability {
	out := []Capability{}
	if e.Command != nil {
		out = append(out, CapabilityCommandLine)
	}
	if e.Placement != nil {
		out = append(out, CapabilityTensorPlacement)
	}
	return out
}

// Has reports whether the engine has a capability.
func (e *Engine) Has(c Capability) bool {
	for _, x := range e.Capabilities() {
		if x == c {
			return true
		}
	}
	return false
}

func (cl *CommandLine) isBinary(word string) bool {
	if cl.IsBinary != nil {
		return cl.IsBinary(word)
	}
	return path.Base(strings.ReplaceAll(word, `\`, "/")) == cl.Binary
}

func (cl *CommandLine) tokenize(s string) ([]string, []string, error) {
	if cl.Tokenize != nil {
		return cl.Tokenize(s)
	}
	return Tokenize(s)
}

// flag finds a table entry by key.
func (e *Engine) flag(key string) *Flag {
	for i := range e.Flags {
		if e.Flags[i].Key == key {
			return &e.Flags[i]
		}
	}
	return nil
}

// optionFlag finds the table entry for an option name or one of its aliases.
// Raw passthrough flags are not options and are never returned.
func (e *Engine) optionFlag(name string) *Flag {
	for i := range e.Flags {
		f := &e.Flags[i]
		if f.Target != TargetOption || f.Raw {
			continue
		}
		if f.Option == name {
			return f
		}
		for _, a := range f.OptionAliases {
			if a == name {
				return f
			}
		}
	}
	return nil
}

// Validate checks that the engine description is complete and consistent.
// Registry.Register calls it, so a broken table fails at start-up and in
// the tests of the engine.
func (e *Engine) Validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if e.ID == "" || e.ID != strings.ToLower(e.ID) || strings.ContainsAny(e.ID, "/ ?#") {
		bad("engine id %q must be a non-empty lower-case path segment", e.ID)
	}
	if e.Name == "" {
		bad("engine %s: the display name is empty", e.ID)
	}
	if len(e.Backends.Names) == 0 && len(e.Backends.Families) == 0 && !e.Backends.Empty {
		bad("engine %s: no backend name", e.ID)
	}
	groups := map[Group]bool{}
	for _, g := range e.Groups {
		groups[g] = true
	}
	keys, cli := map[string]bool{}, map[string]bool{}
	addCLI := func(owner string, spellings []string) {
		for _, c := range spellings {
			if cli[c] {
				bad("engine %s: the command-line spelling %s is used twice (%s)", e.ID, c, owner)
			}
			cli[c] = true
		}
	}
	for _, f := range e.Flags {
		if f.Key == "" || keys[f.Key] {
			bad("engine %s: the flag key %q is empty or used twice", e.ID, f.Key)
		}
		keys[f.Key] = true
		if f.Doc == "" {
			bad("engine %s: flag %s has no doc", e.ID, f.Key)
		}
		if !groups[f.Group] {
			bad("engine %s: flag %s has the unknown group %q", e.ID, f.Key, f.Group)
		}
		switch f.Target {
		case TargetConfig:
			if f.Path == "" || f.Path == "options" {
				bad("engine %s: flag %s needs a config path", e.ID, f.Key)
			}
		case TargetOption:
			if f.Option == "" || f.Path != "options" {
				bad("engine %s: flag %s needs an option name and the path options", e.ID, f.Key)
			}
		default:
			bad("engine %s: flag %s has the unknown target %q", e.ID, f.Key, f.Target)
		}
		if f.Type == TypeEnum && len(f.Enum) == 0 {
			bad("engine %s: enum flag %s lists no value", e.ID, f.Key)
		}
		if e.Command != nil && len(f.CLI) == 0 {
			bad("engine %s: flag %s has no command-line spelling", e.ID, f.Key)
		}
		addCLI(f.Key, append(append([]string{}, f.CLI...), f.CLINegated...))
	}
	if e.Command != nil {
		if e.Command.Binary == "" {
			bad("engine %s: the command line has no binary", e.ID)
		}
		for _, ig := range e.Command.Ignored {
			addCLI("ignored", ig.CLI)
		}
		for _, sp := range e.Command.Special {
			addCLI("special", sp.CLI)
			if sp.Parse == nil {
				bad("engine %s: special flag %v has no parser", e.ID, sp.CLI)
			}
			for _, k := range sp.Covers {
				if !keys[k] {
					bad("engine %s: special flag %v covers the unknown key %s", e.ID, sp.CLI, k)
				}
			}
		}
	}
	for _, c := range e.Combined {
		if !groups[c.Group] {
			bad("engine %s: combined %s has the unknown group %q", e.ID, c.ID, c.Group)
		}
		for _, k := range c.Keys {
			if !keys[k] {
				bad("engine %s: combined %s names the unknown key %s", e.ID, c.ID, k)
			}
		}
		for _, ch := range c.Choices {
			for k := range ch.Set {
				if !keys[k] {
					bad("engine %s: choice %s.%s sets the unknown key %s", e.ID, c.ID, ch.Value, k)
				}
			}
		}
	}
	for _, p := range e.Presets {
		for k, v := range p.Settings {
			f := e.flag(k)
			if f == nil {
				bad("engine %s: preset %s sets the unknown key %s", e.ID, p.ID, k)
				continue
			}
			if _, err := NormalizeValue(f, fmt.Sprint(v)); err != nil {
				bad("engine %s: preset %s: %s: %v", e.ID, p.ID, k, err)
			}
		}
		if p.Placement != nil && e.Placement == nil {
			bad("engine %s: preset %s has a placement but the engine has no placement capability", e.ID, p.ID)
		}
	}
	if pl := e.Placement; pl != nil {
		if pl.Option == "" || pl.Build == nil || pl.Recognise == nil || !groups[pl.Group] {
			bad("engine %s: the placement capability needs an option, a known group, Build and Recognise", e.ID)
		}
	}
	return errors.Join(errs...)
}
