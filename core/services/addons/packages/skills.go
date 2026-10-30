// SPDX-License-Identifier: MIT
package packages

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing/fstest"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// PackageSkill records one skill of a package: every file below
// skills/<Name>/ with its size and digest. The owner copies the files into the
// chats' skill layer and checks each one against this list again, so a
// package directory changed after commit never reaches a chat.
type PackageSkill struct {
	Name  string             `json:"name"`
	Files []PackageSkillFile `json:"files"`
}

// PackageSkillFile is one file of a packaged skill.
type PackageSkillFile struct {
	// Path is relative to the skill folder: SKILL.md or references/x.md.
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Limits of the skill files of one package.
const (
	// MaxSkillReferenceBytes bounds one file of a skill other than SKILL.md,
	// which has skills.MaxFileSize.
	MaxSkillReferenceBytes = 256 << 10
	MaxSkillFiles          = 64
	MaxSkillBytes          = 1 << 20
	MaxPackageSkillBytes   = 4 << 20
	maxSkillPath           = 128
	maxSkillDepth          = 3 // folders below the skill folder
)

// ErrSkill is a package skill that breaks a rule. Its text names the skill and
// the rule, for the administrator.
var ErrSkill = errors.New("invalid add-on skill")

// skillSegment is one segment of a path inside a skill: lowercase ASCII, no
// leading dot. It also keeps out backslashes, percent escapes and spaces.
var skillSegment = regexp.MustCompile(`^[a-z0-9_][a-z0-9._-]*$`)

// skillTypes are the file types a skill may hold. There are no scripts: a
// skill that needs code to run is a built-in skill that requires exec.
var skillTypes = map[string]bool{".md": true, ".txt": true, ".json": true, ".csv": true, ".yaml": true, ".yml": true}

func skillErr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrSkill}, a...)...)
}

// verifySkills checks the skills/ folder of a staged package against the
// manifest and returns the per-skill file list for the receipt. The folders
// under skills/ must be the declared names, every entry a plain file or a
// folder, no file executable, every file an allowed text type within the
// limits, and every SKILL.md a valid skill of its folder's name.
func verifySkills(dir string, m contract.Manifest) ([]PackageSkill, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ErrPackage
	}
	defer func() { _ = root.Close() }()
	defs := m.Capabilities.SkillDefinitions
	info, err := root.Lstat(contract.SkillsDir)
	if len(defs) == 0 {
		if err == nil {
			return nil, skillErr("the package has a %s folder but the manifest declares no skills", contract.SkillsDir)
		}
		return nil, nil
	}
	if err != nil || !info.IsDir() {
		return nil, skillErr("the manifest declares skills but the package has no %s folder", contract.SkillsDir)
	}
	declared := map[string]bool{}
	for _, d := range defs {
		declared[d.Name] = true
	}
	top, err := fs.ReadDir(root.FS(), contract.SkillsDir)
	if err != nil {
		return nil, ErrPackage
	}
	found := map[string]bool{}
	for _, e := range top {
		if !e.IsDir() || !declared[e.Name()] {
			return nil, skillErr("%s/%s is not a declared skill", contract.SkillsDir, e.Name())
		}
		found[e.Name()] = true
	}
	var out []PackageSkill
	var total int64
	for _, d := range defs {
		if !found[d.Name] {
			return nil, skillErr("skill %s: the package has no %s/%s folder", d.Name, contract.SkillsDir, d.Name)
		}
		ps, size, err := verifySkill(root, d.Name)
		if err != nil {
			return nil, err
		}
		total += size
		if total > MaxPackageSkillBytes {
			return nil, skillErr("the skill files are larger than %d bytes", MaxPackageSkillBytes)
		}
		out = append(out, ps)
	}
	return out, nil
}

// verifySkill checks one skill folder and lists its files.
func verifySkill(root *os.Root, name string) (PackageSkill, int64, error) {
	ps := PackageSkill{Name: name}
	base := contract.SkillsDir + "/" + name
	var size int64
	hasSkill := false
	err := fs.WalkDir(root.FS(), base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return ErrPackage
		}
		if p == base {
			return nil
		}
		rel := strings.TrimPrefix(p, base+"/")
		if len(rel) > maxSkillPath || strings.Count(rel, "/") > maxSkillDepth {
			return skillErr("skill %s: the path %s is too long or too deep", name, rel)
		}
		if seg := path.Base(rel); rel != skills.SkillFile && !skillSegment.MatchString(seg) {
			return skillErr("skill %s: the name %s is not allowed", name, rel)
		}
		info, err := root.Lstat(p)
		if err != nil {
			return ErrPackage
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return skillErr("skill %s: %s is not a plain file", name, rel)
		}
		if info.Mode()&0111 != 0 {
			return skillErr("skill %s: %s is executable", name, rel)
		}
		if !skillTypes[strings.ToLower(path.Ext(rel))] {
			return skillErr("skill %s: %s is not an allowed file type (no scripts)", name, rel)
		}
		limit := int64(MaxSkillReferenceBytes)
		if rel == skills.SkillFile {
			limit = skills.MaxFileSize
		}
		if info.Size() > limit {
			return skillErr("skill %s: %s is larger than %d bytes", name, rel, limit)
		}
		data, err := readBounded(root, p, limit)
		if err != nil {
			return ErrPackage
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
		if len(ps.Files) >= MaxSkillFiles || size > MaxSkillBytes {
			return skillErr("skill %s: more than %d files or %d bytes", name, MaxSkillFiles, MaxSkillBytes)
		}
		sum := sha256.Sum256(data)
		ps.Files = append(ps.Files, PackageSkillFile{Path: rel, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return PackageSkill{}, 0, err
	}
	if !hasSkill {
		return PackageSkill{}, 0, skillErr("skill %s: the folder has no SKILL.md", name)
	}
	return ps, size, nil
}

func readBounded(root *os.Root, p string, limit int64) ([]byte, error) {
	f, err := root.Open(p)
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

// SkillFS reads the skill files of a committed package into memory, each one
// checked against the digest the receipt recorded at staging. The result is
// rooted at the folder that holds one folder per skill. A file that changed,
// went away or was added since staging is an error, and nothing is returned.
func SkillFS(r PackageReceipt) (fs.FS, error) {
	out := fstest.MapFS{}
	if len(r.Skills) == 0 {
		return out, nil
	}
	root, err := os.OpenRoot(r.Directory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	listed := 0
	for _, s := range r.Skills {
		for _, f := range s.Files {
			p := contract.SkillsDir + "/" + s.Name + "/" + f.Path
			data, err := readBounded(root, p, f.Size)
			if err != nil {
				return nil, skillErr("skill %s: %s cannot be read", s.Name, f.Path)
			}
			sum := sha256.Sum256(data)
			if int64(len(data)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
				return nil, skillErr("skill %s: %s does not match the receipt", s.Name, f.Path)
			}
			out[s.Name+"/"+f.Path] = &fstest.MapFile{Data: data, Mode: 0o444}
			listed++
		}
	}
	present := 0
	err = fs.WalkDir(root.FS(), contract.SkillsDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			present++
		}
		return nil
	})
	if err != nil || present != listed {
		return nil, skillErr("the skill files of the package changed after it was installed")
	}
	return out, nil
}
