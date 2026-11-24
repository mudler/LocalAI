package skills

import "strings"

// Where finds the skill that a path names, for a model that guessed the
// wrong place to read it. The path looks like a skill path when a folder
// called skills, .skills or ending in -skills is followed by a skill name
// (/workspace/home/.skills/docx/SKILL.md), or when it ends in
// <name>/SKILL.md. The name must be a listed skill that is valid, not
// shadowed and not off. It returns false for any other path, so an unknown
// name gets no suggestion.
func Where(list []Skill, p string) (Skill, bool) {
	segs := strings.Split(strings.Trim(strings.TrimSpace(p), "/"), "/")
	var names []string
	for i, seg := range segs {
		if i+1 < len(segs) && (seg == ".skills" || strings.HasSuffix(seg, "skills")) {
			names = append(names, segs[i+1])
		}
	}
	if n := len(segs); n >= 2 && segs[n-1] == SkillFile {
		names = append(names, segs[n-2])
	}
	for _, name := range names {
		for _, s := range list {
			if s.Name == name && s.Invalid == "" && s.Shadowed == "" && !s.Off && s.GuestPath != "" {
				return s, true
			}
		}
	}
	return Skill{}, false
}
