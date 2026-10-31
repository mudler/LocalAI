package skills

import (
	"fmt"
	"sort"
	"strings"
)

// Limits of the index.
const (
	// MaxIndexEntries is the most skills that Index lists.
	MaxIndexEntries = 100
	// MaxIndexBytes is the most bytes that Index returns.
	MaxIndexBytes = 16 << 10
)

// LoadTool is the name of the tool that returns the instructions of a skill
// by its name. The index tells the model to call it.
const LoadTool = "load_skill"

// indexHeader is the first line of the index. It names the tool that loads a
// skill, so the model never needs the path of a SKILL.md.
const indexHeader = "Skills: before a task that matches one, call the " + LoadTool + " tool with its name to read its full instructions, then follow them. " +
	"Skills you write yourself go in /workspace/home/.skills/<name>/SKILL.md; read the writing-skills skill first (" + LoadTool + " writing-skills)."

// moreReserve is the room kept for the last line, "N more skills not listed".
const moreReserve = 64

// Usable returns the skills that are valid, not shadowed and not off, nearer
// layers first, then names: the skills a chat offers. Name and Description are
// cleaned, so a caller that builds a Skill by hand cannot break a prompt with a
// control character. Index lists them and the load tool finds a skill among
// them, so the two never disagree.
func Usable(skills []Skill) []Skill {
	var list []Skill
	for _, s := range skills {
		if s.Invalid != "" || s.Shadowed != "" || s.Off {
			continue
		}
		s.Name = Clean(s.Name)
		s.Description = Clean(s.Description)
		list = append(list, s)
	}
	sort.SliceStable(list, func(i, j int) bool { return less(list[i], list[j]) })
	return list
}

// Index renders the skills that Usable keeps, for the system prompt: a fixed
// first line that names the load tool, then one line per skill with its name
// and its description (and, for a skill the product marked Unavailable, the
// reason). It names no path: the model loads a skill by its name. Nearer
// layers come first, then names. At most MaxIndexEntries skills and
// MaxIndexBytes bytes are listed; a last line says how many were left out. It
// returns "" when no skill is listed.
func Index(skills []Skill) string {
	list := Usable(skills)
	if len(list) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(indexHeader)
	listed := 0
	for _, s := range list {
		line := "\n- " + s.Name + ": " + s.Description
		if why := Clean(s.Unavailable); why != "" {
			line += " (unavailable here: " + why + ")"
		}
		if listed >= MaxIndexEntries || b.Len()+len(line)+moreReserve > MaxIndexBytes {
			break
		}
		b.WriteString(line)
		listed++
	}
	if left := len(list) - listed; left > 0 {
		fmt.Fprintf(&b, "\n%d more skills not listed", left)
	}
	return b.String()
}
