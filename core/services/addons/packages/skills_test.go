// SPDX-License-Identifier: MIT
package packages

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/skilltree"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func skillMD(name string) []byte {
	return []byte("---\nname: " + name + "\ndescription: Use when the user works with samples.\n---\n# Samples\n\nRead the sample first.\n")
}

var _ = Describe("package skills", func() {
	manifest := func(names ...string) []byte {
		b, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var m contract.Manifest
		Expect(json.Unmarshal(b, &m)).To(Succeed())
		for _, n := range names {
			m.Capabilities.SkillDefinitions = append(m.Capabilities.SkillDefinitions, contract.SkillDefinition{Name: n})
		}
		b, e = json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		return b
	}
	stage := func(names []string, files map[string][]byte, headers ...*tar.Header) (PackageReceipt, error) {
		i, r := fixture(archiveFiles(manifest(names...), files, headers...))
		s, e := i.Stage(context.Background(), r, nil)
		if e != nil {
			return PackageReceipt{}, e
		}
		return i.Commit(context.Background(), s)
	}
	good := func() map[string][]byte {
		return map[string][]byte{
			"skills/sample/SKILL.md":                skillMD("sample"),
			"skills/sample/references/sheets.md":    []byte("# Sheets\n"),
			"skills/sample/references/data/x.json":  []byte(`{"a":1}`),
			"skills/sample-notes/SKILL.md":          skillMD("sample-notes"),
			"skills/sample-notes/references/n.yaml": []byte("a: 1\n"),
		}
	}
	both := []string{"sample", "sample-notes"}

	It("accepts a good skills tree and records every file with its digest", func() {
		receipt, e := stage(both, good())
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.Skills).To(HaveLen(2))
		Expect(receipt.Skills[0].Name).To(Equal("sample"))
		var paths []string
		for _, f := range receipt.Skills[0].Files {
			paths = append(paths, f.Path)
		}
		Expect(paths).To(ConsistOf("SKILL.md", "references/sheets.md", "references/data/x.json"))
		sum := sha256.Sum256(skillMD("sample"))
		for _, f := range receipt.Skills[0].Files {
			if f.Path == "SKILL.md" {
				Expect(f.SHA256).To(Equal(hex.EncodeToString(sum[:])))
				Expect(f.Size).To(Equal(int64(len(skillMD("sample")))))
			}
		}
		// The receipt on disk carries the same list.
		var disk PackageReceipt
		b, e := os.ReadFile(filepath.Join(receipt.Directory, "receipt.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(json.Unmarshal(b, &disk)).To(Succeed())
		Expect(disk.Skills).To(Equal(receipt.Skills))
	})

	It("reads the skills back for the copy, checked against the receipt", func() {
		receipt, e := stage(both, good())
		Expect(e).NotTo(HaveOccurred())
		fsys, e := SkillFS(receipt)
		Expect(e).NotTo(HaveOccurred())
		data, e := fs.ReadFile(fsys, "sample/SKILL.md")
		Expect(e).NotTo(HaveOccurred())
		Expect(data).To(Equal(skillMD("sample")))
		_, e = fs.ReadFile(fsys, "sample-notes/references/n.yaml")
		Expect(e).NotTo(HaveOccurred())

		// A file edited after commit is refused, and nothing is returned.
		target := filepath.Join(receipt.Directory, "skills/sample/references/sheets.md")
		Expect(os.Chmod(target, 0600)).To(Succeed())
		Expect(os.WriteFile(target, []byte("# Changed\n"), 0600)).To(Succeed())
		fsys, e = SkillFS(receipt)
		Expect(e).To(MatchError(ErrSkill))
		Expect(fsys).To(BeNil())
	})

	It("refuses a file added after commit", func() {
		receipt, e := stage(both, good())
		Expect(e).NotTo(HaveOccurred())
		dir := filepath.Join(receipt.Directory, "skills/sample")
		Expect(os.Chmod(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "extra.md"), []byte("x"), 0600)).To(Succeed())
		_, e = SkillFS(receipt)
		Expect(e).To(MatchError(ErrSkill))
	})

	It("has nothing to copy for a package without skills", func() {
		receipt, e := stage(nil, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.Skills).To(BeEmpty())
		fsys, e := SkillFS(receipt)
		Expect(e).NotTo(HaveOccurred())
		entries, e := fs.ReadDir(fsys, ".")
		Expect(e).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	refuse := func(names []string, files map[string][]byte, why string, headers ...*tar.Header) {
		GinkgoHelper()
		_, e := stage(names, files, headers...)
		Expect(e).To(HaveOccurred(), why)
	}
	without := func(drop string) map[string][]byte {
		f := good()
		delete(f, drop)
		return f
	}
	with := func(name string, data []byte) map[string][]byte {
		f := good()
		f[name] = data
		return f
	}

	It("refuses a declared skill without a folder, and a folder that is not declared", func() {
		refuse([]string{"sample"}, good(), "undeclared folder sample-notes")
		f := good()
		delete(f, "skills/sample-notes/SKILL.md")
		delete(f, "skills/sample-notes/references/n.yaml")
		refuse(both, f, "declared skill without a folder")
	})

	It("refuses a skills folder when the manifest declares none", func() {
		refuse(nil, map[string][]byte{"skills/sample/SKILL.md": skillMD("sample")}, "skills without a declaration")
	})

	It("refuses a folder without SKILL.md and bad frontmatter", func() {
		refuse(both, without("skills/sample/SKILL.md"), "no SKILL.md")
		refuse(both, with("skills/sample/SKILL.md", []byte("# no frontmatter\n")), "no frontmatter")
		refuse(both, with("skills/sample/SKILL.md", skillMD("sample-notes")), "name differs from the folder")
		refuse(both, with("skills/sample/SKILL.md", []byte("---\nname: sample\n---\nbody\n")), "no description")
	})

	It("refuses links, executable files and special entries", func() {
		refuse(both, good(), "symlink", &tar.Header{Name: "skills/sample/references/link.md", Typeflag: tar.TypeSymlink, Linkname: "sheets.md", Mode: 0777})
		refuse(both, good(), "hard link", &tar.Header{Name: "skills/sample/references/hard.md", Typeflag: tar.TypeLink, Linkname: "skills/sample/references/sheets.md", Mode: 0644})
		refuse(both, good(), "exec bit", &tar.Header{Name: "skills/sample/references/run.md", Mode: 0755})
	})

	It("refuses scripts and other file types", func() {
		for _, n := range []string{"tool.py", "run.sh", "x.js", "a.exe", "README", "picture.png"} {
			refuse(both, with("skills/sample/references/"+n, []byte("print(1)\n")), n)
		}
	})

	It("refuses names that are not plain lowercase ASCII", func() {
		for _, n := range []string{".hidden.md", "Upper.md", "sp ace.md", "per%41cent.md"} {
			refuse(both, with("skills/sample/references/"+n, []byte("x\n")), n)
		}
	})

	It("refuses text that is not UTF-8 or holds a NUL", func() {
		refuse(both, with("skills/sample/references/bad.md", []byte{0xff, 0xfe, 'a'}), "not UTF-8")
		refuse(both, with("skills/sample/references/nul.md", []byte("a\x00b")), "NUL")
	})

	It("refuses oversize files, deep paths and too many files", func() {
		refuse(both, with("skills/sample/references/big.md", []byte(strings.Repeat("a", skilltree.MaxReferenceBytes+1))), "oversize reference")
		refuse(both, with("skills/sample/SKILL.md", append(skillMD("sample"), []byte(strings.Repeat("a", 64<<10))...)), "oversize SKILL.md")
		refuse(both, with("skills/sample/a/b/c/d/deep.md", []byte("x\n")), "too deep")
		f := good()
		for i := range skilltree.MaxFiles {
			f[fmt.Sprintf("skills/sample/references/f%02d.md", i)] = []byte("x\n")
		}
		refuse(both, f, "too many files")
	})
})
