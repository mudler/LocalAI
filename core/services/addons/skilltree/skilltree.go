// SPDX-License-Identifier: MIT

// Package skilltree checks the skills/ folder of an add-on package against
// its manifest. The owner runs it when it stages a package, and the release
// build runs the same check, so a package that the owner would refuse is never
// published.
package skilltree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Skill records one skill of a package: every file below skills/<Name>/ with
// its size and digest.
type Skill struct {
	Name  string `json:"name"`
	Files []File `json:"files"`
}

// File is one file of a packaged skill.
type File struct {
	// Path is relative to the skill folder: SKILL.md or references/x.md.
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Limits of the skill files of one package.
const (
	// MaxReferenceBytes bounds one file of a skill other than SKILL.md,
	// which has skills.MaxFileSize.
	MaxReferenceBytes = 256 << 10
	MaxFiles          = 64
	MaxSkillBytes     = 1 << 20
	MaxPackageBytes   = 4 << 20
	maxPath           = 128
	maxDepth          = 3 // folders below the skill folder
)

// ErrSkill is a package skill that breaks a rule. The text of the wrapping
// error names the skill and the rule, for the administrator.
var ErrSkill = errors.New("invalid add-on skill")

// segment is one segment of a path inside a skill: lowercase ASCII, no
// leading dot. It also keeps out backslashes, percent escapes and spaces.
var segment = regexp.MustCompile(`^[a-z0-9_][a-z0-9._-]*$`)

// types are the file types a skill may hold. There are no scripts: a skill
// that needs code to run is a built-in skill that requires exec.
var types = map[string]bool{".md": true, ".txt": true, ".json": true, ".csv": true, ".yaml": true, ".yml": true}

func skillErr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrSkill}, a...)...)
}

// Verify checks the skills/ folder of a package against the manifest and
// returns the per-skill file list. dir is that folder, or nil when the package
// has none; its directory entries must report links as links (os.Root.FS and
// os.DirFS do). The folders in it must be the declared names, every entry a
// plain file or a folder, no file executable, every file an allowed text type
// within the limits, and every SKILL.md a valid skill of its folder's name. A
// manifest without skills needs a package without a skills/ folder.
func Verify(dir fs.FS, m contract.Manifest) ([]Skill, error) {
	defs := m.Capabilities.SkillDefinitions
	if len(defs) == 0 {
		if dir != nil {
			return nil, skillErr("the package has a %s folder but the manifest declares no skills", contract.SkillsDir)
		}
		return nil, nil
	}
	if dir == nil {
		return nil, skillErr("the manifest declares skills but the package has no %s folder", contract.SkillsDir)
	}
	declared := map[string]bool{}
	for _, d := range defs {
		declared[d.Name] = true
	}
	top, err := fs.ReadDir(dir, ".")
	if err != nil {
		return nil, skillErr("the %s folder cannot be read", contract.SkillsDir)
	}
	found := map[string]bool{}
	for _, e := range top {
		if !e.IsDir() || !declared[e.Name()] {
			return nil, skillErr("%s/%s is not a declared skill", contract.SkillsDir, e.Name())
		}
		found[e.Name()] = true
	}
	var out []Skill
	var total int64
	for _, d := range defs {
		if !found[d.Name] {
			return nil, skillErr("skill %s: the package has no %s/%s folder", d.Name, contract.SkillsDir, d.Name)
		}
		s, size, err := verifySkill(dir, d.Name)
		if err != nil {
			return nil, err
		}
		total += size
		if total > MaxPackageBytes {
			return nil, skillErr("the skill files are larger than %d bytes", MaxPackageBytes)
		}
		out = append(out, s)
	}
	return out, nil
}

// verifySkill checks one skill folder and lists its files.
func verifySkill(fsys fs.FS, name string) (Skill, int64, error) {
	s := Skill{Name: name}
	base := name
	var size int64
	hasSkill := false
	err := fs.WalkDir(fsys, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return skillErr("skill %s: %s cannot be read", name, p)
		}
		if p == base {
			return nil
		}
		rel := strings.TrimPrefix(p, base+"/")
		if len(rel) > maxPath || strings.Count(rel, "/") > maxDepth {
			return skillErr("skill %s: the path %s is too long or too deep", name, rel)
		}
		if rel != skills.SkillFile && !segment.MatchString(path.Base(rel)) {
			return skillErr("skill %s: the name %s is not allowed", name, rel)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return skillErr("skill %s: %s cannot be read", name, rel)
		}
		if !d.Type().IsRegular() || !info.Mode().IsRegular() {
			return skillErr("skill %s: %s is not a plain file", name, rel)
		}
		if info.Mode()&0o111 != 0 {
			return skillErr("skill %s: %s is executable", name, rel)
		}
		if !types[strings.ToLower(path.Ext(rel))] {
			return skillErr("skill %s: %s is not an allowed file type (no scripts)", name, rel)
		}
		limit := int64(MaxReferenceBytes)
		if rel == skills.SkillFile {
			limit = skills.MaxFileSize
		}
		if info.Size() > limit {
			return skillErr("skill %s: %s is larger than %d bytes", name, rel, limit)
		}
		data, err := ReadBounded(fsys, p, limit)
		if err != nil {
			return skillErr("skill %s: %s cannot be read", name, rel)
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return skillErr("skill %s: %s is not UTF-8 text", name, rel)
		}
		if rel == skills.SkillFile {
			hasSkill = true
			n, _, perr := skills.Parse(data)
			if perr != nil {
				return skillErr("skill %s: SKILL.md: %v", name, perr)
			}
			if n != name {
				return skillErr("skill %s: SKILL.md names the skill %s", name, n)
			}
		}
		size += int64(len(data))
		if len(s.Files) >= MaxFiles || size > MaxSkillBytes {
			return skillErr("skill %s: more than %d files or %d bytes", name, MaxFiles, MaxSkillBytes)
		}
		sum := sha256.Sum256(data)
		s.Files = append(s.Files, File{Path: rel, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return Skill{}, 0, err
	}
	if !hasSkill {
		return Skill{}, 0, skillErr("skill %s: the folder has no SKILL.md", name)
	}
	return s, size, nil
}

// ReadBounded reads the file p of fsys, at most limit bytes; a longer file is
// an error.
func ReadBounded(fsys fs.FS, p string, limit int64) ([]byte, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrSkill
	}
	return data, nil
}
