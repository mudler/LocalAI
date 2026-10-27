package chv

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// debugfsWrite runs debugfs commands that write the image. It needs no root:
// debugfs edits the image file.
func debugfsWrite(t *testing.T, img string, cmds ...string) {
	t.Helper()
	cmd := exec.Command("debugfs", "-w", "-f", "-", img)
	cmd.Stdin = strings.NewReader(strings.Join(cmds, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("debugfs -w: %v\n%s", err, out)
	}
}

// testUpper makes an upper image in the overlay layout without root. It has
// no metadata checksums, so a test can patch a block and the reader still
// sees the patch rather than a checksum error.
func testUpper(t *testing.T) (img string, r upperReader) {
	t.Helper()
	return testUpperFS(t)
}

// testUpperFS is testUpper with more mke2fs arguments, for example -I 128.
func testUpperFS(t *testing.T, extra ...string) (img string, r upperReader) {
	t.Helper()
	needTools(t, "mke2fs", "debugfs")
	dir := t.TempDir()
	img = filepath.Join(dir, "upper.ext4")
	if err := os.WriteFile(img, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(img, 16<<20); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-q", "-F", "-t", "ext4", "-O", "^has_journal,^metadata_csum", "-E", "root_owner=1000:1000"}, extra...)
	if out, err := exec.Command("mke2fs", append(args, img)...).CombinedOutput(); err != nil {
		t.Fatalf("mke2fs: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"new.txt": "new\n", "edit.txt": "edited\n", "x.txt": "x\n"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	debugfsWrite(t, img,
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
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	return img, upperReader{argv: []string{"debugfs", "-c", "-f", "-", img}, stage: stage, host: stage}
}

func summary(t *testing.T, src workspace.FixtureSource) []string {
	t.Helper()
	var out []string
	for _, e := range src {
		s := map[workspace.EntryKind]string{workspace.EntryFile: "file", workspace.EntryDir: "dir", workspace.EntryWhiteout: "whiteout", workspace.EntrySymlink: "symlink", workspace.EntryOther: "other"}[e.Kind] + " " + e.Path
		if e.Kind == workspace.EntryDir && (string(e.Xattrs["trusted.overlay.opaque"]) == "y" || string(e.Xattrs["user.overlay.opaque"]) == "y") {
			s += " opaque"
		}
		if e.Kind == workspace.EntryFile {
			rc, err := e.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			s += " " + strings.TrimSpace(string(b))
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func TestReadUpper(t *testing.T) {
	img, r := testUpper(t)
	ctx := context.Background()
	src, ix, err := r.read(ctx, sandbox.UpperIndex{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"dir u", "dir w", "dir w/d opaque", "file w/d/x.txt x", "file w/edit.txt edited", "file w/new.txt new",
		"other w/fifo", "other w/null", "symlink w/link", "whiteout w/gone.txt",
	}
	if got := summary(t, src); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries:\n%s", strings.Join(got, "\n"))
	}
	// Only trusted.overlay.opaque counts: the guest mounts overlayfs without
	// userxattr, so it ignores user.overlay.opaque and a commit that honoured
	// it would delete files the guest still sees.
	for _, e := range src {
		if e.Path == "u" {
			if _, ok := e.Xattrs["user.overlay.opaque"]; ok {
				t.Fatalf("the reader passed user.overlay.opaque: %v", e.Xattrs)
			}
		}
	}
	// Unchanged files are not sent again, except below an opaque folder.
	src, ix, err = r.read(ctx, ix)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range summary(t, src) {
		if s == "file w/new.txt new" || s == "file w/edit.txt edited" {
			t.Fatalf("an unchanged file was sent again: %s", s)
		}
	}
	if got := summary(t, src); !contains(got, "file w/d/x.txt x") {
		t.Fatalf("a file below an opaque folder was skipped: %v", got)
	}
	// A deleted file that the lower layer never had leaves no whiteout; the
	// index sends one.
	debugfsWrite(t, img, "rm data/w/new.txt", "rm data/w/edit.txt", "write "+filepath.Join(filepath.Dir(img), "src", "new.txt")+" data/w/edit.txt")
	src, _, err = r.read(ctx, ix)
	if err != nil {
		t.Fatal(err)
	}
	got := summary(t, src)
	if !contains(got, "whiteout w/new.txt") || !contains(got, "file w/edit.txt new") {
		t.Fatalf("after the delete: %v", got)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestReadUpperEmptyImage(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img := filepath.Join(t.TempDir(), "upper.ext4")
	if err := os.WriteFile(img, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Truncate(img, 8<<20)
	if out, err := exec.Command("mke2fs", "-q", "-F", "-t", "ext4", "-O", "^has_journal", img).CombinedOutput(); err != nil {
		t.Fatalf("mke2fs: %v\n%s", err, out)
	}
	src, _, err := upperReader{argv: []string{"debugfs", "-c", "-f", "-", img}, stage: t.TempDir()}.read(context.Background(), nil)
	if err != nil || len(src) != 0 {
		t.Fatalf("empty image: %v %+v", err, src)
	}
}
