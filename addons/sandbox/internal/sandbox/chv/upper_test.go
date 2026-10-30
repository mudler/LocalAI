package chv

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

// testUpper makes an upper image in the overlay layout without root. It has
// no metadata checksums, so a test can patch a block and the reader still
// sees the patch rather than a checksum error.
func testUpper() (img string, r upperReader) {
	GinkgoHelper()
	return testUpperFS()
}

// testUpperFS is testUpper with more mke2fs arguments, for example -I 128.
func testUpperFS(extra ...string) (img string, r upperReader) {
	GinkgoHelper()
	needTools("mke2fs", "debugfs")
	dir := GinkgoT().TempDir()
	img = filepath.Join(dir, "upper.ext4")
	Expect(os.WriteFile(img, nil, 0o600)).To(Succeed())
	Expect(os.Truncate(img, 16<<20)).To(Succeed())
	args := append([]string{"-q", "-F", "-t", "ext4", "-O", "^has_journal,^metadata_csum", "-E", "root_owner=1000:1000"}, extra...)
	out, err := exec.Command("mke2fs", append(args, img)...).CombinedOutput()
	Expect(err).To(Succeed(), "mke2fs: %s", out)
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
	stage := filepath.Join(dir, "stage")
	Expect(os.Mkdir(stage, 0o700)).To(Succeed())
	return img, upperReader{argv: []string{"debugfs", "-c", "-f", "-", img}, stage: stage, host: stage}
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

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var _ = Describe("upper image reader", func() {
	It("reads the entries of an upper image", func() {
		img, r := testUpper()
		ctx := context.Background()
		src, ix, err := r.read(ctx, sandbox.UpperIndex{})
		Expect(err).To(Succeed())
		want := []string{
			"dir u", "dir w", "dir w/d opaque", "file w/d/x.txt x", "file w/edit.txt edited", "file w/new.txt new",
			"other w/fifo", "other w/null", "symlink w/link", "whiteout w/gone.txt",
		}
		got := summary(src)
		Expect(strings.Join(got, "\n")).To(Equal(strings.Join(want, "\n")), "entries")
		// Only trusted.overlay.opaque counts: the guest mounts overlayfs without
		// userxattr, so it ignores user.overlay.opaque and a commit that honoured
		// it would delete files the guest still sees.
		for _, e := range src {
			if e.Path == "u" {
				Expect(e.Xattrs).NotTo(HaveKey("user.overlay.opaque"), "the reader passed user.overlay.opaque")
			}
		}
		// Unchanged files are not sent again, except below an opaque folder.
		src, ix, err = r.read(ctx, ix)
		Expect(err).To(Succeed())
		for _, s := range summary(src) {
			Expect(s).NotTo(Or(Equal("file w/new.txt new"), Equal("file w/edit.txt edited")), "an unchanged file was sent again")
		}
		got = summary(src)
		Expect(contains(got, "file w/d/x.txt x")).To(BeTrue(), "a file below an opaque folder was skipped: %v", got)
		// A deleted file that the lower layer never had leaves no whiteout; the
		// index sends one.
		debugfsWrite(img, "rm data/w/new.txt", "rm data/w/edit.txt", "write "+filepath.Join(filepath.Dir(img), "src", "new.txt")+" data/w/edit.txt")
		src, _, err = r.read(ctx, ix)
		Expect(err).To(Succeed())
		got = summary(src)
		Expect(contains(got, "whiteout w/new.txt")).To(BeTrue(), "after the delete: %v", got)
		Expect(contains(got, "file w/edit.txt new")).To(BeTrue(), "after the delete: %v", got)
	})

	It("reads an empty image", func() {
		needTools("mke2fs", "debugfs")
		img := filepath.Join(GinkgoT().TempDir(), "upper.ext4")
		Expect(os.WriteFile(img, nil, 0o600)).To(Succeed())
		_ = os.Truncate(img, 8<<20)
		out, err := exec.Command("mke2fs", "-q", "-F", "-t", "ext4", "-O", "^has_journal", img).CombinedOutput()
		Expect(err).To(Succeed(), "mke2fs: %s", out)
		src, _, err := upperReader{argv: []string{"debugfs", "-c", "-f", "-", img}, stage: GinkgoT().TempDir()}.read(context.Background(), nil)
		Expect(err).To(Succeed())
		Expect(src).To(BeEmpty(), "empty image")
	})
})
