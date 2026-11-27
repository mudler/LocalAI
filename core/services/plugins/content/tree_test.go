package content_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/content"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func refusedWith(err error, code string) {
	GinkgoHelper()
	Expect(errors.Is(err, content.ErrRefused)).To(BeTrue(), "got %v", err)
	var ce *content.CheckError
	Expect(errors.As(err, &ce)).To(BeTrue(), "got %v", err)
	Expect(ce.Code).To(Equal(code), "got %v", err)
}

var _ = Describe("Check", func() {
	lim := content.DefaultLimits()

	It("lists every file with its size, mode and hash, sorted by path", func() {
		dir := tree(map[string]string{
			".claude-plugin/plugin.json": `{"name":"fmt"}`,
			"skills/fmt/SKILL.md":        "# fmt",
			"bin/fmt":                    "+x#!/bin/sh\necho hi\n",
			"scripts/run.py":             "#!/usr/bin/env python3\n",
		})
		t, err := content.Check(dir, lim)
		Expect(err).NotTo(HaveOccurred())
		paths := []string{}
		for _, f := range t.Files {
			paths = append(paths, f.Path)
		}
		Expect(paths).To(Equal([]string{".claude-plugin/plugin.json", "bin/fmt", "scripts/run.py", "skills/fmt/SKILL.md"}))
		Expect(t.Files[1].Mode).To(Equal(content.ModeExec))
		Expect(t.Files[1].Script).To(BeTrue())
		Expect(t.Files[2].Mode).To(Equal(content.ModeFile))
		Expect(t.Files[2].Script).To(BeTrue(), "a #! first line marks a script")
		Expect(t.Files[3].Script).To(BeFalse())
		Expect(t.Files[3].SHA256).To(Equal(sum("# fmt")))
		Expect(t.Files[3].Size).To(BeEquivalentTo(5))
		Expect(t.Size).To(BeEquivalentTo(len(`{"name":"fmt"}`) + 5 + len("#!/bin/sh\necho hi\n") + len("#!/usr/bin/env python3\n")))
	})

	It("leaves out .git folders", func() {
		dir := tree(map[string]string{"skills/a/SKILL.md": "a", ".git/HEAD": "ref", "sub/.git/config": "x"})
		t, err := content.Check(dir, lim)
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Files).To(HaveLen(1))
	})

	DescribeTable("refuses a path that is not a clean relative path",
		func(p, code string) { refusedWith(content.CheckRelPath(p), code) },
		Entry("parent segment", "../x", content.CodeTraversal),
		Entry("inner parent segment", "a/../../b", content.CodeTraversal),
		Entry("absolute", "/etc/passwd", content.CodeAbsolute),
		Entry("backslash", `a\b`, content.CodeBackslash),
		Entry("NUL", "a\x00b", content.CodeNUL),
		Entry("drive letter", "C:/x", content.CodeDrive),
		Entry("drive letter without slash", "c:x", content.CodeDrive),
		Entry("invalid UTF-8", "a\xffb", content.CodeBadName),
		Entry("control character", "a\x01b", content.CodeBadName),
		Entry("right-to-left override", "scripts/run\u202Ehs.txt", content.CodeFormatChar),
		Entry("bidi isolate", "a\u2066b", content.CodeFormatChar),
		Entry("zero-width space", "scripts/ch\u200Beck.sh", content.CodeFormatChar),
		Entry("zero-width joiner", "a\u200Db", content.CodeFormatChar),
		Entry("byte order mark", "\uFEFFa", content.CodeFormatChar),
		Entry("empty segment", "a//b", content.CodeBadName),
		Entry("dot segment", "a/./b", content.CodeBadName),
		Entry("empty", "", content.CodeBadName),
	)

	It("accepts a clean relative path", func() {
		Expect(content.CheckRelPath("skills/fmt/SKILL.md")).To(Succeed())
		Expect(content.CheckRelPath(".mcp.json")).To(Succeed())
	})

	It("refuses a file name with a backslash", func() {
		refusedWith(checkErr(tree(map[string]string{`a\b.md`: "x"}), lim), content.CodeBackslash)
	})

	It("refuses a symlink that leaves the tree", func() {
		outside := tree(map[string]string{"secret": "s"})
		dir := tree(map[string]string{"a.md": "a"})
		Expect(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "link"))).To(Succeed())
		refusedWith(checkErr(dir, lim), content.CodeSymlink)

		dir2 := tree(map[string]string{"a.md": "a"})
		Expect(os.Symlink("../../../../../../etc/hostname", filepath.Join(dir2, "rel"))).To(Succeed())
		refusedWith(checkErr(dir2, lim), content.CodeSymlink)
	})

	It("stores a symlink inside the tree as a regular file", func() {
		dir := tree(map[string]string{"skills/a/SKILL.md": "body", "bin/real": "+x#!/bin/sh\n"})
		Expect(os.Symlink("skills/a/SKILL.md", filepath.Join(dir, "link.md"))).To(Succeed())
		Expect(os.Symlink("../bin/real", filepath.Join(dir, "skills", "tool"))).To(Succeed())
		t, err := content.Check(dir, lim)
		Expect(err).NotTo(HaveOccurred())
		byPath := map[string]content.File{}
		for _, f := range t.Files {
			byPath[f.Path] = f
		}
		Expect(byPath).To(HaveKey("link.md"))
		Expect(byPath["link.md"].SHA256).To(Equal(sum("body")))
		Expect(byPath["skills/tool"].Mode).To(Equal(content.ModeExec), "the mode comes from the target")
	})

	It("refuses a symlink to a folder", func() {
		dir := tree(map[string]string{"skills/a/SKILL.md": "body"})
		Expect(os.Symlink("skills", filepath.Join(dir, "loop"))).To(Succeed())
		refusedWith(checkErr(dir, lim), content.CodeSymlink)
	})

	It("refuses two paths that differ only in case", func() {
		refusedWith(checkErr(tree(map[string]string{"README.md": "a", "readme.md": "b"}), lim), content.CodeCaseCollision)
		refusedWith(checkErr(tree(map[string]string{"Skills/a.md": "a", "skills/a.md": "b"}), lim), content.CodeCaseCollision)
	})

	It("refuses more files than the limit", func() {
		l := lim
		l.MaxFiles = 2
		refusedWith(checkErr(tree(map[string]string{"a": "1", "b": "2", "c": "3"}), l), content.CodeTooManyFiles)
	})

	It("refuses a file over the file limit", func() {
		l := lim
		l.MaxFileBytes = 4
		refusedWith(checkErr(tree(map[string]string{"a": "12345"}), l), content.CodeFileTooLarge)
	})

	It("refuses a tree over the total limit", func() {
		l := lim
		l.MaxTotalBytes = 8
		refusedWith(checkErr(tree(map[string]string{"a": "12345", "b": "12345"}), l), content.CodeTooLarge)
	})

	It("refuses a tree deeper than the limit", func() {
		l := lim
		l.MaxDepth = 3
		Expect(content.Check(tree(map[string]string{"a/b/c": "x"}), l)).Error().NotTo(HaveOccurred())
		refusedWith(checkErr(tree(map[string]string{"a/b/c/d": "x"}), l), content.CodeTooDeep)
	})

	It("refuses a path longer than the limit", func() {
		l := lim
		l.MaxPathBytes = 10
		refusedWith(checkErr(tree(map[string]string{"abcdef/ghijk": "x"}), l), content.CodePathTooLong)
	})

	It("has the limits of the design by default", func() {
		Expect(lim.MaxFiles).To(Equal(2000))
		Expect(lim.MaxFileBytes).To(BeEquivalentTo(5 << 20))
		Expect(lim.MaxTotalBytes).To(BeEquivalentTo(20 << 20))
		Expect(lim.MaxDepth).To(Equal(16))
		Expect(lim.MaxPathBytes).To(Equal(255))
		Expect(lim.MaxTextBytes).To(BeEquivalentTo(64 << 10))
		Expect(lim.MaxConfigBytes).To(BeEquivalentTo(256 << 10))
	})

	DescribeTable("caps the files of each component",
		func(p string, limit int, capped bool) {
			ok := tree(map[string]string{p: strings.Repeat("x", limit)})
			Expect(content.Check(ok, lim)).Error().NotTo(HaveOccurred())
			over := tree(map[string]string{p: strings.Repeat("x", limit+1)})
			if capped {
				refusedWith(checkErr(over, lim), content.CodeComponentTooLarge)
			} else {
				Expect(content.Check(over, lim)).Error().NotTo(HaveOccurred())
			}
		},
		Entry("SKILL.md", "skills/a/SKILL.md", 64<<10, true),
		Entry("a command", "commands/db/migrate.md", 64<<10, true),
		Entry("an agent", "agents/review.md", 64<<10, true),
		Entry("plugin.json", ".claude-plugin/plugin.json", 256<<10, true),
		Entry("hooks.json", "hooks/hooks.json", 256<<10, true),
		Entry(".mcp.json", ".mcp.json", 256<<10, true),
		Entry("another file of a skill", "skills/a/reference.md", 64<<10, false),
	)
})

var _ = Describe("Digest", func() {
	It("hashes the sorted lines of path, mode and content hash", func() {
		dir := tree(map[string]string{"b.md": "B", "a.sh": "+xA"})
		t, err := content.Check(dir, content.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		want := sha256.Sum256([]byte("a.sh\x000755\x00" + sum("A") + "\n" + "b.md\x000644\x00" + sum("B") + "\n"))
		Expect(content.Digest(t)).To(Equal("sha256:" + hex.EncodeToString(want[:])))
	})

	It("does not depend on the order of the files", func() {
		t := content.Tree{Files: []content.File{
			{Path: "a", Mode: content.ModeFile, SHA256: sum("1")},
			{Path: "b", Mode: content.ModeExec, SHA256: sum("2")},
		}}
		r := content.Tree{Files: []content.File{t.Files[1], t.Files[0]}}
		Expect(content.Digest(r)).To(Equal(content.Digest(t)))
	})

	It("is equal for equal trees and differs when a mode or a byte changes", func() {
		files := map[string]string{"skills/a/SKILL.md": "a", "bin/x": "+xx"}
		t1, _ := content.Check(tree(files), content.DefaultLimits())
		t2, _ := content.Check(tree(files), content.DefaultLimits())
		Expect(content.Digest(t1)).To(Equal(content.Digest(t2)))

		t3, _ := content.Check(tree(map[string]string{"skills/a/SKILL.md": "a", "bin/x": "x"}), content.DefaultLimits())
		Expect(content.Digest(t3)).NotTo(Equal(content.Digest(t1)))
		t4, _ := content.Check(tree(map[string]string{"skills/a/SKILL.md": "b", "bin/x": "+xx"}), content.DefaultLimits())
		Expect(content.Digest(t4)).NotTo(Equal(content.Digest(t1)))
	})

	It("differs when the effective manifest differs", func() {
		t, _ := content.Check(tree(map[string]string{"skills/a/SKILL.md": "a"}), content.DefaultLimits())
		bare := content.Digest(t)
		t.Manifest = sum("one")
		one := content.Digest(t)
		t.Manifest = sum("two")
		Expect(one).NotTo(Equal(bare))
		Expect(content.Digest(t)).NotTo(Equal(one))
	})
})
