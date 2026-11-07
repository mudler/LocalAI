package skills

import (
	"archive/zip"
	"bytes"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type zent struct {
	name string
	body string
	mode uint32 // unix mode bits, 0 for a regular file
}

func makeZip(ents ...zent) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(fsMode(e.mode))
		}
		w, err := zw.CreateHeader(h)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte(e.body))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(zw.Close()).To(Succeed())
	return buf.Bytes()
}

func skillMD(name string) string {
	return "---\nname: " + name + "\ndescription: Does a thing. Use when asked.\nlicense: MIT\nmetadata:\n  author: me\n  version: \"1.0\"\nallowed-tools: Bash(git:*) Read\n---\n# Steps\n"
}

func read(b []byte, lim PackLimits) ([]Pack, error) {
	return ReadPacks(bytes.NewReader(b), int64(len(b)), lim)
}

var _ = Describe("ReadPacks", func() {
	It("reads one skill with SKILL.md at the top", func() {
		packs, err := read(makeZip(zent{name: "SKILL.md", body: skillMD("pdf-tools")}, zent{name: "scripts/run.py", body: "print(1)"}, zent{name: "references/a.md", body: "x"}), PackLimits{})
		Expect(err).NotTo(HaveOccurred())
		Expect(packs).To(HaveLen(1))
		Expect(packs[0].Name).To(Equal("pdf-tools"))
		Expect(packs[0].Description).To(Equal("Does a thing. Use when asked."))
		var paths []string
		for _, f := range packs[0].Files {
			paths = append(paths, f.Path)
		}
		Expect(paths).To(Equal([]string{"SKILL.md", "references/a.md", "scripts/run.py"}))
	})

	It("reads several skills, one per folder, and ignores archiver metadata", func() {
		packs, err := read(makeZip(
			zent{name: "a/", body: ""}, zent{name: "a/SKILL.md", body: skillMD("a")},
			zent{name: "b/SKILL.md", body: skillMD("b")}, zent{name: "b/assets/logo.png", body: "png"},
			zent{name: "__MACOSX/a/._SKILL.md", body: "junk"}, zent{name: "a/.DS_Store", body: "junk"},
		), PackLimits{})
		Expect(err).NotTo(HaveOccurred())
		Expect(packs).To(HaveLen(2))
		Expect(packs[0].Name).To(Equal("a"))
		Expect(packs[0].Files).To(HaveLen(1))
		Expect(packs[1].Files).To(HaveLen(2))
	})

	DescribeTable("refuses an archive that is not valid",
		func(ents []zent, lim PackLimits, sentinel error, text string) {
			_, err := read(makeZip(ents...), lim)
			Expect(err).To(MatchError(sentinel))
			Expect(err.Error()).To(ContainSubstring(text))
		},
		Entry("no SKILL.md", []zent{{name: "a/readme.md", body: "x"}}, PackLimits{}, ErrPackBad, "no SKILL.md"),
		Entry("file at the top beside folders", []zent{{name: "a/SKILL.md", body: skillMD("a")}, {name: "notes.txt", body: "x"}}, PackLimits{}, ErrPackBad, "at the top"),
		Entry("empty archive", nil, PackLimits{}, ErrPackBad, "no files"),
		Entry("uppercase name", []zent{{name: "SKILL.md", body: skillMD("PDF")}}, PackLimits{}, ErrPackBad, "not valid"),
		Entry("leading hyphen", []zent{{name: "SKILL.md", body: skillMD("-pdf")}}, PackLimits{}, ErrPackBad, "not valid"),
		Entry("trailing hyphen", []zent{{name: "SKILL.md", body: skillMD("pdf-")}}, PackLimits{}, ErrPackBad, "not valid"),
		Entry("double hyphen", []zent{{name: "SKILL.md", body: skillMD("pdf--x")}}, PackLimits{}, ErrPackBad, "not valid"),
		Entry("name too long", []zent{{name: "SKILL.md", body: skillMD(strings.Repeat("a", 65))}}, PackLimits{}, ErrPackBad, "not valid"),
		Entry("folder differs from name", []zent{{name: "other/SKILL.md", body: skillMD("a")}}, PackLimits{}, ErrPackBad, "same name"),
		Entry("no description", []zent{{name: "SKILL.md", body: "---\nname: a\n---\n"}}, PackLimits{}, ErrPackBad, "needs a description"),
		Entry("no frontmatter", []zent{{name: "SKILL.md", body: "# hi"}}, PackLimits{}, ErrPackBad, "frontmatter"),
		Entry("metadata value not text", []zent{{name: "SKILL.md", body: "---\nname: a\ndescription: d\nmetadata:\n  n: 1\n---\n"}}, PackLimits{}, ErrPackBad, "metadata"),
		Entry("compatibility too long", []zent{{name: "SKILL.md", body: "---\nname: a\ndescription: d\ncompatibility: " + strings.Repeat("x", 501) + "\n---\n"}}, PackLimits{}, ErrPackBad, "compatibility"),
		Entry("description too long", []zent{{name: "SKILL.md", body: "---\nname: a\ndescription: " + strings.Repeat("x", 1025) + "\n---\n"}}, PackLimits{}, ErrPackBad, "description"),
		Entry("path traversal", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "../evil.sh", body: "x"}}, PackLimits{}, ErrPackBad, ".."),
		Entry("traversal in the middle", []zent{{name: "a/SKILL.md", body: skillMD("a")}, {name: "a/../../evil", body: "x"}}, PackLimits{}, ErrPackBad, ".."),
		Entry("absolute path", []zent{{name: "/etc/passwd", body: "x"}}, PackLimits{}, ErrPackBad, "absolute"),
		Entry("backslash", []zent{{name: "a\\b", body: "x"}}, PackLimits{}, ErrPackBad, "backslash"),
		Entry("drive letter", []zent{{name: "C:/x", body: "x"}}, PackLimits{}, ErrPackBad, "not allowed"),
		Entry("symlink", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "link", body: "/etc/passwd", mode: modeSymlinkBits}}, PackLimits{}, ErrPackBad, "link"),
		Entry("duplicate path", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "x.md", body: "1"}, {name: "x.md", body: "2"}}, PackLimits{}, ErrPackBad, "same name"),
		Entry("case collision", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "References/a.md", body: "1"}, {name: "references/a.md", body: "2"}}, PackLimits{}, ErrPackBad, "same name"),
		Entry("file below a file", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "x", body: "1"}, {name: "x/y", body: "2"}}, PackLimits{}, ErrPackBad, "below it"),
		Entry("too many entries", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "1", body: "x"}, {name: "2", body: "x"}}, PackLimits{MaxEntries: 2}, ErrPackLimit, "entries"),
		Entry("file over the limit", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "big", body: strings.Repeat("x", 2000)}}, PackLimits{MaxFileBytes: 1000}, ErrPackLimit, "larger"),
		Entry("total over the limit", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "b1", body: strings.Repeat("x", 600)}, {name: "b2", body: strings.Repeat("x", 600)}}, PackLimits{MaxTotalBytes: 1000}, ErrPackLimit, "unpacks"),
		Entry("too deep", []zent{{name: "SKILL.md", body: skillMD("a")}, {name: "1/2/3/4/5/6/7/8/9/f", body: "x"}}, PackLimits{}, ErrPackBad, "deeper"),
		Entry("SKILL.md over 64 KiB", []zent{{name: "SKILL.md", body: skillMD("a") + strings.Repeat("x", MaxFileSize)}}, PackLimits{}, ErrPackBad, "larger"),
		Entry("the same skill twice", []zent{{name: "a/SKILL.md", body: skillMD("a")}, {name: "A/SKILL.md", body: skillMD("a")}}, PackLimits{}, ErrPackBad, "same name"),
	)

	It("rejects a zip bomb by the bytes really read", func() {
		// Highly compressible: 30 MiB of zeros deflates to a few KiB.
		_, err := read(makeZip(zent{name: "SKILL.md", body: skillMD("a")}, zent{name: "z", body: strings.Repeat("\x00", 30<<20)}), PackLimits{})
		Expect(err).To(MatchError(ErrPackLimit))
	})

	It("is not a zip", func() {
		_, err := read([]byte("not a zip"), PackLimits{})
		Expect(err).To(MatchError(ErrPackBad))
	})

	It("reads a description that continues on indented lines", func() {
		md := "---\nname: a\ndescription: First part\n  second part.\n---\n"
		packs, err := read(makeZip(zent{name: "SKILL.md", body: md}), PackLimits{})
		Expect(err).NotTo(HaveOccurred())
		Expect(packs[0].Description).To(Equal("First part second part."))
	})
})
