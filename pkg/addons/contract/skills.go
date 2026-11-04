// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
	"strings"
)

// MaxSkillDefinitions bounds the skills one add-on ships. The chat's skill
// index has room for 100 lines and 16 KiB; one add-on must not fill it.
const MaxSkillDefinitions = 16

// SkillsDir is the folder of a package that holds one folder per skill:
// skills/<name>/SKILL.md plus optional reference files.
const SkillsDir = "skills"

// SkillDefinition declares one skill the package ships under
// skills/<Name>/. The description is not here: the SKILL.md frontmatter is
// its one source.
type SkillDefinition struct {
	// Name is the skill's name and the name of its folder. A plain skill is
	// named after the add-on: the add-on ID, or the ID and a dash and more
	// (office-quickstart), so two add-ons never ship one plain name. A skill
	// for a reserved document slot has the slot's name (see Shadows).
	Name string `json:"name"`
	// Shadows names the document-provider slot this skill supplies. It must equal Name
	// and name a slot: one the skill declares (Slot) or a default one
	// (DefaultSlots). It repeats the name on purpose: the
	// replacement is an explicit act, which the install warning and the check
	// read from the manifest without opening the package. Empty: a plain
	// add-on skill.
	Shadows string `json:"shadows,omitempty"`
	// Slot describes the slot this skill serves: the files it covers and the
	// words to say about them (see SlotDefinition). It needs Shadows. A skill that shadows a default slot may leave it out.
	Slot *SlotDefinition `json:"slot,omitempty"`
}

// protectedSkills are the built-in skills that no add-on may replace or
// ship under their name: they describe LocalAI itself. A test derives the
// built-in names from the embedded tree and fails when one is neither here
// nor a reserved document slot.
var protectedSkills = []string{"localai-workspace", "localai-apps", "writing-skills", "memory", "memory-check-own-files"}

// LegacyMemoryAddonID is the add-on that used to ship the two memory skills.
// They are built into the host now; the package keeps declaring them so an
// installed copy stays valid, and the host does not load its copies (the
// built-in skills win).
const LegacyMemoryAddonID = "memory"

// LegacyMemorySkill reports whether name is one of the memory skills that the
// add-on id declared before they became built-in.
func LegacyMemorySkill(addonID, name string) bool {
	return addonID == LegacyMemoryAddonID && (name == "memory" || name == "memory-check-own-files")
}

// ProtectedSkill reports whether name is a built-in skill that an add-on may
// not replace.
func ProtectedSkill(name string) bool {
	return slices.Contains(protectedSkills, name)
}

// DocumentSkill reports whether name is a default document slot (slots.json).
// An add-on can declare more slots of its own; the host learns them from the
// skills it lists, not from here.
func DocumentSkill(name string) bool {
	s, ok := DefaultSlot(name)
	return ok && s.Kind == SlotKindDocuments
}

// ReplaceableSkills lists the default slots.
var ReplaceableSkills = func() []string {
	var out []string
	for _, s := range defaultSlots() {
		out = append(out, s.Name)
	}
	return out
}()

// ReplaceableSkill is the compatibility name for DocumentSkill.
func ReplaceableSkill(name string) bool { return DocumentSkill(name) }

// BuiltinSkillName reports the protected core skill names, not provider slots.
func BuiltinSkillName(name string) bool { return ProtectedSkill(name) }

// PlainSkillName reports whether name is a plain skill name the add-on id may
// ship: the id itself, or the id followed by a dash and more.
func PlainSkillName(addonID, name string) bool {
	return name == addonID || strings.HasPrefix(name, addonID+"-")
}

// validateSkillDefinitions checks the skills of the add-on id: bounded, well
// formed, unique, and named after the add-on.
func validateSkillDefinitions(id string, defs []SkillDefinition) error {
	if len(defs) == 0 {
		return nil
	}
	if len(defs) > MaxSkillDefinitions {
		return fmt.Errorf("too many skill definitions: at most %d", MaxSkillDefinitions)
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if !namespace.MatchString(d.Name) {
			return fmt.Errorf("skill %q: not a valid skill name", d.Name)
		}
		if seen[d.Name] {
			return fmt.Errorf("skill %s: declared twice", d.Name)
		}
		seen[d.Name] = true
		if d.Shadows != "" {
			switch {
			case d.Shadows != d.Name:
				return fmt.Errorf("skill %s: shadows must repeat the skill's name", d.Name)
			case ProtectedSkill(d.Shadows):
				return fmt.Errorf("skill %s: %q is a built-in skill that an add-on may replace in no way", d.Name, d.Shadows)
			case d.Slot == nil && !ReplaceableSkill(d.Shadows):
				return fmt.Errorf("skill %s: %q is not a slot an add-on may replace; declare its slot", d.Name, d.Shadows)
			}
			if d.Slot != nil {
				if err := d.Slot.validate(d.Name); err != nil {
					return err
				}
			}
			continue
		}
		if d.Slot != nil {
			return fmt.Errorf("skill %s: a slot needs shadows", d.Name)
		}
		if DocumentSkill(d.Name) {
			return fmt.Errorf("skill %s: reserved document name requires same-name shadows", d.Name)
		}
		if BuiltinSkillName(d.Name) && !LegacyMemorySkill(id, d.Name) {
			return fmt.Errorf("skill %s: the name is a built-in skill", d.Name)
		}
		if !PlainSkillName(id, d.Name) {
			return fmt.Errorf("skill %s: a skill name must be %s or start with %s-", d.Name, id, id)
		}
	}
	return nil
}

// ShadowedSkills lists, in definition order, the document slots supplied by
// the skill definitions.
func ShadowedSkills(defs []SkillDefinition) []string {
	var out []string
	for _, d := range defs {
		if _, ok := d.slot(); ok {
			out = append(out, d.Shadows)
		}
	}
	return out
}

// SkillNames lists the names of the skill definitions, in order.
func SkillNames(defs []SkillDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}
