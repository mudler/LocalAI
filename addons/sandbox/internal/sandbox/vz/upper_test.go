package vz

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4mk"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// debugfsWrite runs debugfs commands that write the image. It needs no root:
// debugfs edits the image file.
func debugfsWrite(img string, cmds ...string) {
	GinkgoHelper()
	cmd := exec.Command("debugfs", "-w", "-f", "-", img)
	cmd.Stdin = strings.NewReader(strings.Join(cmds, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	Expect(err).To(Succeed(), "debugfs -w: %s", out)
}

// upperKind says how a test makes its upper image.
type upperKind struct {
	name string
	make func(path string)
}

// Both makers give a valid ext4 image of the overlay layout. The first is the
// one the backend makes. The second is mke2fs with its default features
// (extents, 64-bit, metadata checksums), which is what an image from another
// tool looks like.
var upperKinds = []upperKind{
	{"made by ext4mk", func(path string) {
		GinkgoHelper()
		Expect(ext4mk.FormatFile(path, ext4mk.Options{Size: 32 << 20, RootUID: 1000, RootGID: 1000})).To(Succeed())
	}},
	{"made by mke2fs with checksums", func(path string) {
		GinkgoHelper()
		needTools("mke2fs")
		Expect(os.WriteFile(path, nil, 0o600)).To(Succeed())
		Expect(os.Truncate(path, 32<<20)).To(Succeed())
		out, err := exec.Command("mke2fs", "-q", "-F", "-t", "ext4", "-O", "^has_journal", "-E", "root_owner=1000:1000", path).CombinedOutput()
		Expect(err).To(Succeed(), "mke2fs: %s", out)
	}},
}

// testUpper makes an upper image in the overlay layout, as the guest leaves it
// after some work, and a folder for the staged files.
func testUpper(kind upperKind) (img, stage string) {
	GinkgoHelper()
	needTools("debugfs")
	dir := GinkgoT().TempDir()
	img = filepath.Join(dir, "upper.ext4")
	kind.make(img)
	src := filepath.Join(dir, "src")
	Expect(os.Mkdir(src, 0o755)).To(Succeed())
	for name, content := range map[string]string{"new.txt": "new\n", "edit.txt": "edited\n", "x.txt": "x\n"} {
		Expect(os.WriteFile(filepath.Join(src, name), []byte(content), 0o644)).To(Succeed())
	}
	debugfsWrite(img,
		"mkdir data", "mkdir work", "mkdir data/w",
		"write "+src+"/new.txt data/w/new.txt",
		"write "+src+"/edit.txt data/w/edit.txt",
		"cd data/w", "mknod gone.txt c 0 0", "mknod null c 1 3", "mknod fifo p", "cd /",
		"mkdir data/w/d", "ea_set data/w/d trusted.overlay.opaque y",
		"write "+src+"/x.txt data/w/d/x.txt",
		"symlink data/w/link new.txt",
		"mkdir data/u", "ea_set data/u user.overlay.opaque y",
	)
	stage = filepath.Join(dir, "stage")
	Expect(os.Mkdir(stage, 0o700)).To(Succeed())
	return img, stage
}

func summary(src workspace.FixtureSource) []string {
	GinkgoHelper()
	var out []string
	for _, e := range src {
		s := map[workspace.EntryKind]string{workspace.EntryFile: "file", workspace.EntryDir: "dir", workspace.EntryWhiteout: "whiteout", workspace.EntrySymlink: "symlink", workspace.EntryOther: "other"}[e.Kind] + " " + e.Path
		if e.Kind == workspace.EntryDir && (string(e.Xattrs["trusted.overlay.opaque"]) == "y" || string(e.Xattrs["user.overlay.opaque"]) == "y") {
			s += " opaque"
		}
		if e.Kind == workspace.EntryFile {
			rc, err := e.Open()
			Expect(err).To(Succeed())
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			s += " " + strings.TrimSpace(string(b))
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func readCtx() context.Context {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	DeferCleanup(cancel)
	return ctx
}

var _ = Describe("upper image reader", func() {
	for _, kind := range upperKinds {
		Describe(kind.name, func() {
			It("reads the entries of an upper image", func() {
				img, stage := testUpper(kind)
				ctx := readCtx()
				src, ix, err := readUpper(ctx, img, stage, readLimits{}, sandbox.UpperIndex{})
				Expect(err).To(Succeed())
				want := []string{
					"dir u", "dir w", "dir w/d opaque", "file w/d/x.txt x", "file w/edit.txt edited", "file w/new.txt new",
					"other w/fifo", "other w/null", "symlink w/link", "whiteout w/gone.txt",
				}
				Expect(strings.Join(summary(src), "\n")).To(Equal(strings.Join(want, "\n")), "entries")
				// Only trusted.overlay.opaque counts: the guest mounts overlayfs
				// without userxattr, so it ignores user.overlay.opaque, and a
				// commit that honoured it would delete files the guest still sees.
				for _, e := range src {
					if e.Path == "u" {
						Expect(e.Xattrs).NotTo(HaveKey("user.overlay.opaque"), "the reader passed user.overlay.opaque")
					}
				}
				// Unchanged files are not sent again, except below an opaque folder.
				Expect(os.RemoveAll(stage)).To(Succeed())
				Expect(os.Mkdir(stage, 0o700)).To(Succeed())
				src, ix, err = readUpper(ctx, img, stage, readLimits{}, ix)
				Expect(err).To(Succeed())
				got := summary(src)
				Expect(got).NotTo(ContainElement("file w/new.txt new"), "an unchanged file was sent again")
				Expect(got).NotTo(ContainElement("file w/edit.txt edited"), "an unchanged file was sent again")
				Expect(got).To(ContainElement("file w/d/x.txt x"), "a file below an opaque folder was skipped")
				// A deleted file that the lower layer never had leaves no whiteout;
				// the index sends one.
				debugfsWrite(img, "rm data/w/new.txt", "rm data/w/edit.txt", "write "+filepath.Join(filepath.Dir(img), "src", "new.txt")+" data/w/edit.txt")
				Expect(os.RemoveAll(stage)).To(Succeed())
				Expect(os.Mkdir(stage, 0o700)).To(Succeed())
				src, _, err = readUpper(ctx, img, stage, readLimits{}, ix)
				Expect(err).To(Succeed())
				got = summary(src)
				Expect(got).To(ContainElement("whiteout w/new.txt"))
				Expect(got).To(ContainElement("file w/edit.txt new"))
			})

			It("reads an empty image", func() {
				img := filepath.Join(GinkgoT().TempDir(), "upper.ext4")
				kind.make(img)
				src, _, err := readUpper(readCtx(), img, GinkgoT().TempDir(), readLimits{}, nil)
				Expect(err).To(Succeed())
				Expect(src).To(BeEmpty(), "empty image")
			})

			It("stages a file once for its hard links and with its exact size", func() {
				img, stage := testUpper(kind)
				debugfsWrite(img, "link data/w/new.txt data/w/again.txt")
				src, _, err := readUpper(readCtx(), img, stage, readLimits{}, nil)
				Expect(err).To(Succeed())
				got := summary(src)
				Expect(got).To(ContainElements("file w/new.txt new", "file w/again.txt new"))
				ents, _ := os.ReadDir(stage)
				// new.txt, edit.txt, d/x.txt: three inodes, four paths.
				Expect(ents).To(HaveLen(3), "one staged copy for each inode")
			})

			It("refuses a stage that goes over its limit before it copies anything", func() {
				img, stage := testUpper(kind)
				_, _, err := readUpper(readCtx(), img, stage, readLimits{stage: 5}, nil)
				Expect(err).To(MatchError(ContainSubstring("goes over the stage limit")))
				ents, _ := os.ReadDir(stage)
				Expect(ents).To(BeEmpty(), "a file was staged before the limit check")
			})

			It("fails on a directory loop", func() {
				img, stage := testUpper(kind)
				debugfsWrite(img, "link data/w/d data/w/d/again")
				start := time.Now()
				_, _, err := readUpper(readCtx(), img, stage, readLimits{}, nil)
				Expect(err).To(MatchError(ContainSubstring("w/d/again")), "a directory loop")
				Expect(time.Since(start)).To(BeNumerically("<=", 10*time.Second))
			})

			It("fails on a layer deeper than the cap and on one with too many entries", func() {
				img, stage := testUpper(kind)
				debugfsWrite(img, "mkdir data/a", "mkdir data/a/b", "mkdir data/a/b/c", "mkdir data/a/b/c/d")
				_, _, err := readUpper(readCtx(), img, stage, readLimits{depth: 3}, nil)
				Expect(err).To(MatchError(ContainSubstring("deeper than 3 levels")))
				_, _, err = readUpper(readCtx(), img, stage, readLimits{entries: 5}, nil)
				Expect(err).To(MatchError(ContainSubstring("more than 5 entries")))
				// The caps leave a normal image alone.
				_, _, err = readUpper(readCtx(), img, stage, readLimits{depth: 8, entries: 50}, nil)
				Expect(err).To(Succeed())
			})

			It("fails on an inode outside the file system", func() {
				img, stage := testUpper(kind)
				// A directory entry that points at inode 2^31 is hostile.
				debugfsWrite(img, "set_inode_field data/w/new.txt links_count 1")
				raw, err := os.ReadFile(img)
				Expect(err).To(Succeed())
				// Find the record of "new.txt" in a directory block and change its inode.
				i := indexOfRecord(raw, "new.txt")
				Expect(i).To(BeNumerically(">", 0), "the directory record was not found")
				raw[i], raw[i+1], raw[i+2], raw[i+3] = 0xff, 0xff, 0xff, 0x7f
				Expect(os.WriteFile(img, raw, 0o600)).To(Succeed())
				_, _, err = readUpper(readCtx(), img, stage, readLimits{}, nil)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("inode")))
			})

			It("fails cleanly on a corrupt superblock", func() {
				img, stage := testUpper(kind)
				raw, err := os.ReadFile(img)
				Expect(err).To(Succeed())
				raw[1024+56], raw[1024+57] = 0, 0
				Expect(os.WriteFile(img, raw, 0o600)).To(Succeed())
				_, _, err = readUpper(readCtx(), img, stage, readLimits{}, nil)
				Expect(err).To(HaveOccurred())
			})
		})
	}

	It("stops when the context ends", func() {
		img, stage := testUpper(upperKinds[0])
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := readUpper(ctx, img, stage, readLimits{}, nil)
		Expect(err).To(HaveOccurred())
	})
})

// indexOfRecord finds the inode field of the directory record that has the
// given name: a record is inode (4), rec_len (2), name_len (1), type (1) and
// the name.
func indexOfRecord(raw []byte, name string) int {
	n := []byte(name)
	for i := 0; i+8+len(n) < len(raw); i += 4 {
		if raw[i+6] == byte(len(n)) && slices.Equal(raw[i+8:i+8+len(n)], n) && raw[i+4] != 0 {
			return i
		}
	}
	return -1
}
