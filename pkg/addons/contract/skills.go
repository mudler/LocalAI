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
	// that replaces a built-in one has the built-in's name (see Shadows).
	Name string `json:"name"`
	// Shadows names the built-in skill this skill replaces. It must equal Name
	// and be one of ReplaceableSkills. It repeats the name on purpose: the
	// replacement is an explicit act, which the install warning and the check
	// read from the manifest without opening the package. Empty: a plain
	// add-on skill.
	Shadows string `json:"shadows,omitempty"`
}

// protectedSkills are the built-in skills that no add-on may replace or
// ship under their name: they describe LocalAI itself. A test derives the
// built-in names from the embedded tree and fails when one is neither here
// nor replaceable.
var protectedSkills = []string{"localai-workspace", "localai-apps", "writing-skills"}

// ProtectedSkill reports whether name is a built-in skill that an add-on may
// not replace.
func ProtectedSkill(name string) bool {
	return slices.Contains(protectedSkills, name)
}

// ReplaceableSkills are the built-in skills an add-on skill may replace by
// name. They describe a way to work with office files; an add-on that brings
// its own tools for those files brings the procedure that matches them.
var ReplaceableSkills = []string{"xlsx", "docx", "pptx", "pdf"}

// ReplaceableSkill reports whether an add-on skill may replace the built-in
// skill name.
func ReplaceableSkill(name string) bool {
	return slices.Contains(ReplaceableSkills, name)
}

// BuiltinSkillName reports whether name is the name of a skill that ships with
// the host.
func BuiltinSkillName(name string) bool { return ProtectedSkill(name) || ReplaceableSkill(name) }

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
			case !ReplaceableSkill(d.Shadows):
				return fmt.Errorf("skill %s: %q is not a built-in skill an add-on may replace", d.Name, d.Shadows)
			}
			continue
		}
		if BuiltinSkillName(d.Name) {
			return fmt.Errorf("skill %s: the name is a built-in skill", d.Name)
		}
		if !PlainSkillName(id, d.Name) {
			return fmt.Errorf("skill %s: a skill name must be %s or start with %s-", d.Name, id, id)
		}
	}
	return nil
}

// ShadowedSkills lists, in definition order, the built-in skills that the skill
// definitions replace.
func ShadowedSkills(defs []SkillDefinition) []string {
	var out []string
	for _, d := range defs {
		if d.Shadows != "" && d.Shadows == d.Name && ReplaceableSkill(d.Shadows) {
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
