package enginesettings

import (
	"errors"
	"fmt"
)

// CapabilityDrafting: the engine describes its speculative decoding as a few
// modes (off, n-grams, a draft model...), so a client can offer one choice
// instead of the raw flags.
const CapabilityDrafting Capability = "drafting"

// NeedsMTPHead is the DraftMode.Needs value of a mode that runs only on a
// model whose file has a multi-token prediction head.
const NeedsMTPHead = "mtp_head"

// Drafting is the drafting capability. The modes write the flags of the
// table, so a file written by hand, a pasted command and the YAML tab all
// read back as a mode.
type Drafting struct {
	// Group is the editor section the modes are drawn in. A client draws
	// its other flags under the modes as "more options".
	Group Group `json:"group"`
	// TypeKey is the flag key of the drafting kind (a comma-separated list).
	TypeKey string `json:"type_key"`
	// ModelKey is the flag key of the draft model file.
	ModelKey string `json:"model_key"`
	// OffType is the kind Off writes when the model would draft without
	// one (a mode with Auto). Empty: Off always writes no kind.
	OffType string `json:"off_type,omitempty"`
	// Streams is the load limit of drafting. Optional.
	Streams *DraftStreams `json:"streams,omitempty"`
	// Modes are the choices, the first one being Off (no kind, no model).
	Modes []DraftMode `json:"modes"`
}

// DraftStreams is an option that pauses drafting under load. It is a plain
// option: a backend build without it ignores the entry, so it need not be in
// the table. When the table has it, it must be LocalAI only.
type DraftStreams struct {
	Option  string `json:"option"`
	Default string `json:"default"`
	Doc     string `json:"doc"`
}

// DraftMode is one choice of the drafting capability.
type DraftMode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Doc   string `json:"doc"`
	// Note is the trade-off of the mode, said plainly. Optional.
	Note string `json:"note,omitempty"`
	// Types are the kinds read back as this mode. The first is written
	// when the mode is picked. Empty for Off and for a draft model.
	Types []string `json:"types,omitempty"`
	// Model: the mode needs a draft model file (ModelKey).
	Model bool `json:"model,omitempty"`
	// Needs names what the model must have, such as NeedsMTPHead. A client
	// offers the mode only when the model has it, or when the file already
	// uses the mode.
	Needs string `json:"needs,omitempty"`
	// Auto: the backend turns the mode on by itself for a model that has
	// what Needs names when the file sets no kind.
	Auto bool `json:"auto,omitempty"`
	// Set lists flag values written when the mode is picked and the flag
	// is unset, such as the window LocalAI uses for MTP.
	Set map[string]any `json:"set,omitempty"`
	// Fields are the flag keys drawn under the mode, in order.
	Fields []string `json:"fields,omitempty"`
}

// validateDrafting checks the drafting capability against the table.
func (e *Engine) validateDrafting(groups map[Group]bool, keys map[string]bool) error {
	d := e.Drafting
	if d == nil {
		return nil
	}
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !groups[d.Group] {
		bad("engine %s: the drafting group %q is not a group", e.ID, d.Group)
	}
	if !keys[d.TypeKey] || !keys[d.ModelKey] {
		bad("engine %s: drafting needs a known type key and model key (%q, %q)", e.ID, d.TypeKey, d.ModelKey)
	}
	if s := d.Streams; s != nil {
		if s.Option == "" || s.Doc == "" {
			bad("engine %s: the drafting streams option needs a name and a doc", e.ID)
		}
		if f := e.optionFlag(s.Option); f != nil && !f.LocalAIOnly {
			bad("engine %s: the drafting streams option %s is in the table but not LocalAI only", e.ID, s.Option)
		}
	}
	if len(d.Modes) == 0 || len(d.Modes[0].Types) > 0 || d.Modes[0].Model {
		bad("engine %s: the first drafting mode must be Off (no kind and no model)", e.ID)
	}
	ids := map[string]bool{}
	for _, m := range d.Modes {
		if m.ID == "" || ids[m.ID] || m.Label == "" || m.Doc == "" {
			bad("engine %s: drafting mode %q needs a new id, a label and a doc", e.ID, m.ID)
		}
		ids[m.ID] = true
		if m.Auto && m.Needs == "" {
			bad("engine %s: drafting mode %s is automatic but needs nothing", e.ID, m.ID)
		}
		for _, k := range m.Fields {
			if !keys[k] {
				bad("engine %s: drafting mode %s draws the unknown key %s", e.ID, m.ID, k)
			}
		}
		for k, v := range m.Set {
			f := e.flag(k)
			if f == nil {
				bad("engine %s: drafting mode %s sets the unknown key %s", e.ID, m.ID, k)
				continue
			}
			if _, err := NormalizeValue(f, fmt.Sprint(v)); err != nil {
				bad("engine %s: drafting mode %s: %s: %v", e.ID, m.ID, k, err)
			}
		}
	}
	return errors.Join(errs...)
}

// isDraftStreams reports whether an option name is the drafting streams
// option, which a build reads with no flag.
func (e *Engine) isDraftStreams(name string) bool {
	return e.Drafting != nil && e.Drafting.Streams != nil && e.Drafting.Streams.Option == name
}
