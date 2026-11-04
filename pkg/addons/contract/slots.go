// SPDX-License-Identifier: MIT
package contract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// SlotKindDocuments is the kind of a slot for a document format: the skill
// that serves it needs a commands provider that reports isolated execution.
const SlotKindDocuments = SkillKindDocuments

// Bounds of a slot declaration.
const (
	MaxSlotExtensions = 8
	MaxSlotWords      = 8
	MaxSlotWordChars  = 32
	MaxSlotLibChars   = 64
)

// SlotDefinition describes the slot a skill serves. The add-on that provides
// the skill declares it, so the host needs no list of formats. It carries no
// authority: it only tells the host which files the skill covers and what to
// say when the host meets one.
type SlotDefinition struct {
	// Kind is SlotKindDocuments.
	Kind string `json:"kind"`
	// Extensions are the file extensions the skill covers, lower case with the
	// dot, such as ".docx".
	Extensions []string `json:"extensions,omitempty"`
	// Words are lower-case words that a skill of this slot has in its name.
	Words []string `json:"words,omitempty"`
	// Library names what a script can use to read these files, shown as a hint.
	Library string `json:"library,omitempty"`
}

// Slot is a named slot: the name of the skill that serves it and its
// definition.
type Slot struct {
	Name string `json:"name"`
	SlotDefinition
}

//go:embed slots.json
var defaultSlotsJSON []byte

var defaultSlots = sync.OnceValue(func() []Slot {
	var out []Slot
	if err := json.Unmarshal(defaultSlotsJSON, &out); err != nil {
		panic("contract: slots.json: " + err.Error())
	}
	return out
})

// DefaultSlots are the slots the host knows without any declaration, read
// from data. A skill that shadows one of them and declares no slot of its own
// is served by these.
func DefaultSlots() []Slot { return slices.Clone(defaultSlots()) }

// DefaultSlot returns the default slot called name.
func DefaultSlot(name string) (Slot, bool) {
	i := slices.IndexFunc(defaultSlots(), func(s Slot) bool { return s.Name == name })
	if i < 0 {
		return Slot{}, false
	}
	return defaultSlots()[i], true
}

// SlotsOf lists the slots the skill definitions serve, in order: the slot a
// skill declares, or else the default slot of the name it shadows.
func SlotsOf(defs []SkillDefinition) []Slot {
	var out []Slot
	for _, d := range defs {
		if s, ok := d.slot(); ok {
			out = append(out, s)
		}
	}
	return out
}

// slot returns the slot this skill serves.
func (d SkillDefinition) slot() (Slot, bool) {
	if d.Shadows == "" || d.Shadows != d.Name {
		return Slot{}, false
	}
	if d.Slot != nil {
		return Slot{Name: d.Name, SlotDefinition: *d.Slot}, true
	}
	return DefaultSlot(d.Name)
}

// validate checks a slot declaration of the skill name.
func (s *SlotDefinition) validate(name string) error {
	if s.Kind != SlotKindDocuments {
		return fmt.Errorf("skill %s: unknown slot kind %q", name, s.Kind)
	}
	if len(s.Extensions) > MaxSlotExtensions || len(s.Words) > MaxSlotWords {
		return fmt.Errorf("skill %s: too many slot extensions or words", name)
	}
	for _, e := range s.Extensions {
		if len(e) < 2 || len(e) > 16 || e[0] != '.' || strings.ToLower(e) != e || !plainText(e, 16) || strings.ContainsAny(e[1:], "./\\ ") {
			return fmt.Errorf("skill %s: invalid slot extension %q", name, e)
		}
	}
	for _, w := range s.Words {
		if !plainText(w, MaxSlotWordChars) || strings.ToLower(w) != w {
			return fmt.Errorf("skill %s: invalid slot word", name)
		}
	}
	if s.Library != "" && !plainText(s.Library, MaxSlotLibChars) {
		return fmt.Errorf("skill %s: invalid slot library", name)
	}
	return nil
}
