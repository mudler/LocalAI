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

// indexHeader is the first line of the index.
const indexHeader = "Skills: before a task that matches one, read its SKILL.md with the read_file tool at the path shown (the Sandbox does not see skill folders). " +
	"Skills you write yourself go in /workspace/home/.skills/<name>/SKILL.md; read the writing-skills skill first."

// moreReserve is the room kept for the last line, "N more skills not listed".
const moreReserve = 64

// Index renders the skills that are valid, not shadowed and not off, for the
// system prompt: a fixed first line, then one line per skill with its name,
// its description and the guest path of its SKILL.md (or, for a skill the
// product marked Unavailable, the reason instead of the path). Nearer layers come
// first, then names. At most MaxIndexEntries skills and MaxIndexBytes bytes
// are listed; a last line says how many were left out. It returns "" when no
// skill is listed. Name and Description are cleaned, so a caller that builds
// a Skill by hand cannot break the block with a control character.
func Index(skills []Skill) string {
	var list []Skill
	for _, s := range skills {
		if s.Invalid != "" || s.Shadowed != "" || s.Off {
			continue
		}
		s.Name = Clean(s.Name)
		s.Description = Clean(s.Description)
		list = append(list, s)
	}
	if len(list) == 0 {
		return ""
	}
	sort.SliceStable(list, func(i, j int) bool { return less(list[i], list[j]) })

	var b strings.Builder
	b.WriteString(indexHeader)
	listed := 0
	for _, s := range list {
		line := "\n- " + s.Name + ": " + s.Description
		if why := Clean(s.Unavailable); why != "" {
			line += " (unavailable here: " + why + ")"
		} else if gp := Clean(s.GuestPath); gp != "" {
			line += " (read " + gp + ")"
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
