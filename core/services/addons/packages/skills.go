// SPDX-License-Identifier: MIT
package packages

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/addons/skilltree"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// PackageSkill records one skill of a package: every file below
// skills/<Name>/ with its size and digest. The owner copies the files into the
// chats' skill layer and checks each one against this list again, so a
// package directory changed after commit never reaches a chat.
type PackageSkill = skilltree.Skill

// PackageSkillFile is one file of a packaged skill.
type PackageSkillFile = skilltree.File

// ErrSkill is a package skill that breaks a rule (see skilltree.Verify).
var ErrSkill = skilltree.ErrSkill

// verifySkills checks the skills/ folder of a staged package against the
// manifest, without following links, and returns the per-skill file list for
// the receipt.
func verifySkills(dir string, m contract.Manifest) ([]PackageSkill, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ErrPackage
	}
	defer func() { _ = root.Close() }()
	var skills fs.FS
	if info, err := root.Lstat(contract.SkillsDir); err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: %s is not a folder", ErrSkill, contract.SkillsDir)
		}
		if skills, err = fs.Sub(root.FS(), contract.SkillsDir); err != nil {
			return nil, ErrPackage
		}
	}
	return skilltree.Verify(skills, m)
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
	fsys := root.FS()
	listed := 0
	for _, s := range r.Skills {
		for _, f := range s.Files {
			p := contract.SkillsDir + "/" + s.Name + "/" + f.Path
			data, err := skilltree.ReadBounded(fsys, p, f.Size)
			if err != nil {
				return nil, fmt.Errorf("%w: skill %s: %s cannot be read", ErrSkill, s.Name, f.Path)
			}
			sum := sha256.Sum256(data)
			if int64(len(data)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
				return nil, fmt.Errorf("%w: skill %s: %s does not match the receipt", ErrSkill, s.Name, f.Path)
			}
			out[s.Name+"/"+f.Path] = &fstest.MapFile{Data: data, Mode: 0o444}
			listed++
		}
	}
	present := 0
	err = fs.WalkDir(fsys, contract.SkillsDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			present++
		}
		return nil
	})
	if err != nil || present != listed {
		return nil, fmt.Errorf("%w: the skill files of the package changed after it was installed", ErrSkill)
	}
	return out, nil
}
