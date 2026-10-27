package convert

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type oldSkill struct {
	user   string // "" with shared set is the shared layer
	shared bool
	name   string
	text   string // the SKILL.md text
	files  int    // resource files that are not converted
	git    bool
}

// parseSkill reads a SKILL.md: frontmatter with a name and a description, then
// the body. ok is false when there is no description.
func parseSkill(text string) (name, description, body string, ok bool) {
	t := strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(t, "---\n") {
		return "", "", "", false
	}
	rest := t[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", "", false
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			name = v
		case "description":
			description = v
		}
	}
	body = strings.TrimPrefix(rest[end+4:], "\n")
	return name, description, body, description != ""
}

func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() != "SKILL.md" {
			n++
		}
		return nil
	})
	return n
}

func (r *run) readSkills() []oldSkill {
	var out []oldSkill
	scan := func(base, user string, shared bool) {
		entries, err := os.ReadDir(base)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(base, e.Name(), "SKILL.md"))
			if err != nil {
				continue
			}
			out = append(out, oldSkill{user: user, shared: shared, name: e.Name(), text: string(raw), files: countFiles(filepath.Join(base, e.Name()))})
		}
	}
	if r.src.StateDir != "" {
		scan(filepath.Join(r.src.StateDir, "skills"), "", true)
		for _, uid := range r.userDirs() {
			scan(filepath.Join(r.src.StateDir, "users", uid, "skills", "skills"), uid, false)
		}
	}
	if hasTable(r.src.DB, "skills_metadata") {
		var rows []struct {
			UserID     string `gorm:"column:user_id"`
			Name       string `gorm:"column:name"`
			Definition string `gorm:"column:definition"`
			SourceType string `gorm:"column:source_type"`
		}
		if err := r.src.DB.Table("skills_metadata").Order("user_id, name").Find(&rows).Error; err != nil {
			r.add(KindSkill, "", "skills_metadata", StatusFailed, "could not read the table: "+err.Error())
		} else {
			for _, row := range rows {
				out = append(out, oldSkill{user: row.UserID, shared: row.UserID == "", name: row.Name, text: row.Definition, git: row.SourceType == "git"})
			}
		}
	}
	return out
}

func (r *run) skillsStep(ctx context.Context) error {
	for _, s := range r.readSkills() {
		r.convertSkill(ctx, s)
	}
	return nil
}

func (r *run) convertSkill(ctx context.Context, s oldSkill) {
	owner := ""
	if !s.shared {
		var why string
		if owner, why = r.owner(ctx, s.user); why != "" {
			r.add(KindSkill, s.user, s.name, StatusSkipped, why)
			return
		}
	}
	layer := "shared"
	if !s.shared {
		layer = s.user
	}
	key := "skill|" + layer + "|" + s.name
	if r.handled(ctx, key, KindSkill, s.user, s.name) {
		return
	}
	if s.git {
		r.drop(ctx, key, KindSkill, s.user, s.name, "a skill from a git repository is not converted: add the repository's skills by hand")
		return
	}
	name, desc, body, ok := parseSkill(s.text)
	if !ok {
		r.drop(ctx, key, KindSkill, s.user, s.name, "SKILL.md has no description in its frontmatter")
		return
	}
	if name == "" {
		name = s.name
	}
	if exists, err := r.tg.SkillExists(ctx, owner, s.shared, name); err == nil && exists {
		r.mark(ctx, key, refExists)
		r.add(KindSkill, s.user, s.name, StatusSkipped, reasonExists)
		return
	}
	if err := r.tg.WriteSkill(ctx, owner, s.shared, name, desc, body); err != nil {
		r.refused(ctx, key, KindSkill, s.user, s.name, err)
		return
	}
	r.mark(ctx, key, name)
	reason := ""
	if s.files > 0 {
		reason = "resource files are not converted: " + plural(s.files, "file")
	}
	r.add(KindSkill, s.user, s.name, StatusConverted, reason)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strings.TrimSpace(strings.Join([]string{itoa(n), word + "s"}, " "))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
