//go:build linux

package gvisor

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

type entry struct {
	h    tar.Header
	body string
}

func tarOf(t *testing.T, entries ...entry) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		e.h.Size = int64(len(e.body))
		if e.h.Typeflag != tar.TypeReg {
			e.h.Size = 0
		}
		if err := tw.WriteHeader(&e.h); err != nil {
			t.Fatal(err)
		}
		if e.h.Typeflag == tar.TypeReg {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestUnpackKeepsWhatTheSandboxNeeds(t *testing.T) {
	dir := t.TempDir()
	// Registered first: the image has a read-only folder, which RemoveAll of
	// the plain temp dir could not empty if a later step failed.
	t.Cleanup(func() { _ = removeAll(dir) })
	err := unpack(tarOf(t,
		entry{h: tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}},
		entry{h: tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o555}},
		entry{h: tar.Header{Name: "opt/suid", Typeflag: tar.TypeReg, Mode: 0o4755, Uid: 12345, Gid: 12345}, body: "#!/bin/sh\n"},
		entry{h: tar.Header{Name: "opt/sgid", Typeflag: tar.TypeReg, Mode: 0o2755}, body: "x"},
		entry{h: tar.Header{Name: "opt/ro", Typeflag: tar.TypeReg, Mode: 0o444}, body: "ro"},
		entry{h: tar.Header{Name: "opt/link", Typeflag: tar.TypeSymlink, Linkname: "suid", Mode: 0o777}},
		entry{h: tar.Header{Name: "opt/abs-link", Typeflag: tar.TypeSymlink, Linkname: "/usr/bin/env", Mode: 0o777}},
		entry{h: tar.Header{Name: "opt/hard", Typeflag: tar.TypeLink, Linkname: "opt/ro"}},
		entry{h: tar.Header{Name: "opt/fifo", Typeflag: tar.TypeFifo, Mode: 0o600}},
		entry{h: tar.Header{Name: "opt/null", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 3}},
		entry{h: tar.Header{Name: "tmp/", Typeflag: tar.TypeDir, Mode: 0o1777}},
		entry{h: tar.Header{Name: "late/dir/file", Typeflag: tar.TypeReg, Mode: 0o644}, body: "parent made on the way"},
		entry{h: tar.Header{Name: "mnt/", Typeflag: tar.TypeDir, Mode: 0o755}},
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "rootfs")
	mode := func(p string) os.FileMode {
		t.Helper()
		st, err := os.Lstat(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		return st.Mode()
	}
	if m := mode("opt/suid"); m&(os.ModeSetuid|os.ModeSetgid) != 0 || m.Perm() != 0o755 {
		t.Errorf("suid file mode = %v", m)
	}
	if m := mode("opt/sgid"); m&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Errorf("sgid file mode = %v", m)
	}
	if m := mode("opt/ro"); m.Perm() != 0o444 {
		t.Errorf("ro mode = %v", m)
	}
	if m := mode("opt"); m.Perm() != 0o555 {
		t.Errorf("a read-only folder has mode %v", m)
	}
	if m := mode("tmp"); m&os.ModeSticky == 0 || m.Perm() != 0o777 {
		t.Errorf("tmp mode = %v", m)
	}
	if target, _ := os.Readlink(filepath.Join(root, "opt/link")); target != "suid" {
		t.Errorf("link -> %q", target)
	}
	if target, _ := os.Readlink(filepath.Join(root, "opt/abs-link")); target != "/usr/bin/env" {
		t.Errorf("absolute link -> %q", target)
	}
	a, _ := os.Stat(filepath.Join(root, "opt/ro"))
	b, _ := os.Stat(filepath.Join(root, "opt/hard"))
	if !os.SameFile(a, b) {
		t.Error("the hard link is a copy")
	}
	for _, skipped := range []string{"opt/fifo", "opt/null"} {
		if _, err := os.Lstat(filepath.Join(root, skipped)); !os.IsNotExist(err) {
			t.Errorf("%s was not skipped: %v", skipped, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(root, "late/dir/file")); string(got) != "parent made on the way" {
		t.Errorf("late file = %q", got)
	}
	for _, d := range []string{"mnt/lower", "mnt/upper", "mnt/ctl"} {
		if st, err := os.Stat(filepath.Join(root, d)); err != nil || !st.IsDir() {
			t.Errorf("mount point %s: %v", d, err)
		}
	}
	// Every file belongs to the user who unpacked it, whatever the tar says.
	for _, p := range []string{"opt/ro", "opt/suid"} {
		st, _ := os.Stat(filepath.Join(root, p))
		if uid := st.Sys().(*syscall.Stat_t).Uid; int(uid) != os.Getuid() {
			t.Errorf("%s belongs to uid %d", p, uid)
		}
	}
}

func TestUnpackRefusesHostileEntries(t *testing.T) {
	// outside is the absolute path of a folder next to the cache.
	cases := map[string]func(outside string) []entry{
		"absolute name": func(outside string) []entry {
			return []entry{{h: tar.Header{Name: outside + "/evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"}}
		},
		"dot dot name": func(string) []entry {
			return []entry{{h: tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"}}
		},
		"write through a symlink": func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: "../outside"}},
				{h: tar.Header{Name: "esc/evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
			}
		},
		"write through an absolute symlink": func(outside string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: outside}},
				{h: tar.Header{Name: "esc/evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
			}
		},
		"hard link out of the folder": func(string) []entry {
			return []entry{{h: tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "../outside/sentinel"}}}
		},
		"hard link through a symlinked folder": func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: "../outside"}},
				{h: tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "esc/sentinel"}},
			}
		},
		"folder through a symlink": func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: "../outside"}},
				{h: tar.Header{Name: "esc/sub/", Typeflag: tar.TypeDir, Mode: 0o755}},
			}
		},
		"folder replacing a file": func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
				{h: tar.Header{Name: "f/", Typeflag: tar.TypeDir, Mode: 0o755}},
			}
		},
		"very long name": func(string) []entry {
			return []entry{{h: tar.Header{Name: strings.Repeat("a", 300), Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"}}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			t.Cleanup(func() { _ = removeAll(parent) })
			outside := filepath.Join(parent, "outside")
			if err := os.Mkdir(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(parent, "cache")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := unpack(tarOf(t, mk(outside)...), dir); err == nil {
				t.Fatal("the hostile image was unpacked")
			}
			// Nothing was written outside the cache folder.
			if ents, _ := os.ReadDir(parent); len(ents) != 2 {
				t.Fatalf("the folder next to the cache changed: %v", ents)
			}
			ents, _ := os.ReadDir(outside)
			if len(ents) != 1 || ents[0].Name() != "sentinel" {
				t.Fatalf("something was written outside the folder: %v", ents)
			}
			if got, _ := os.ReadFile(sentinel); string(got) != "keep" {
				t.Fatalf("sentinel = %q", got)
			}
			if st, err := os.Stat(sentinel); err != nil || st.Sys().(*syscall.Stat_t).Nlink != 1 {
				t.Fatalf("the sentinel got a hard link: %v", err)
			}
			// A failed unpack leaves no folder behind.
			if _, err := os.Lstat(filepath.Join(dir, "rootfs")); !os.IsNotExist(err) {
				t.Fatalf("the failed unpack left rootfs: %v", err)
			}
		})
	}
}

func TestUnpackLetsAFileReplaceAFolder(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { _ = removeAll(dir) })
	err := unpack(tarOf(t,
		entry{h: tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}},
		entry{h: tar.Header{Name: "d/inner", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
		entry{h: tar.Header{Name: "d", Typeflag: tar.TypeReg, Mode: 0o644}, body: "now a file"},
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "rootfs", "d")); string(got) != "now a file" {
		t.Fatalf("d = %q", got)
	}
}

func TestUnpackDoesNotChmodThroughAReplacedFolder(t *testing.T) {
	// A folder entry that a later entry replaces by a symlink: the final
	// folder modes must not follow the link.
	t.Run("link to a file in the root", func(t *testing.T) {
		dir := t.TempDir()
		t.Cleanup(func() { _ = removeAll(dir) })
		err := unpack(tarOf(t,
			entry{h: tar.Header{Name: "target", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
			entry{h: tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o500}},
			entry{h: tar.Header{Name: "d", Typeflag: tar.TypeSymlink, Linkname: "target"}},
		), dir)
		if err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(filepath.Join(dir, "rootfs", "target"))
		if err != nil || st.Mode().Perm() != 0o644 {
			t.Fatalf("target = %v, %v", st, err)
		}
	})
	t.Run("dangling link", func(t *testing.T) {
		dir := t.TempDir()
		t.Cleanup(func() { _ = removeAll(dir) })
		err := unpack(tarOf(t,
			entry{h: tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o500}},
			entry{h: tar.Header{Name: "d", Typeflag: tar.TypeSymlink, Linkname: "missing"}},
		), dir)
		if err != nil {
			t.Fatal(err)
		}
		if target, _ := os.Readlink(filepath.Join(dir, "rootfs", "d")); target != "missing" {
			t.Fatalf("d -> %q", target)
		}
	})
}

func TestFailedUnpackLeavesARemovableFolder(t *testing.T) {
	parent := t.TempDir()
	t.Cleanup(func() { _ = ociimage.RemoveTree(parent) })
	dir := filepath.Join(parent, "cache")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// "mnt" is a file, so the step after the entries fails, with a
	// read-only folder already unpacked.
	err := unpack(tarOf(t,
		entry{h: tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o555}},
		entry{h: tar.Header{Name: "opt/f", Typeflag: tar.TypeReg, Mode: 0o444}, body: "x"},
		entry{h: tar.Header{Name: "mnt", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
	), dir)
	if err == nil {
		t.Fatal("unpack worked")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("the cache folder cannot be removed: %v", err)
	}
}

func TestRemoveTreeRemovesAnUnpackedReadOnlyFolder(t *testing.T) {
	parent := t.TempDir()
	t.Cleanup(func() { _ = ociimage.RemoveTree(parent) })
	dir := filepath.Join(parent, "cache")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := unpack(tarOf(t,
		entry{h: tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o555}},
		entry{h: tar.Header{Name: "opt/deep/", Typeflag: tar.TypeDir, Mode: 0o000}},
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ociimage.RemoveTree(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir still there: %v", err)
	}
}

func TestUnpackStopsAtTheEntryLimit(t *testing.T) {
	old := maxUnpackEntries
	maxUnpackEntries = 3
	t.Cleanup(func() { maxUnpackEntries = old })
	var es []entry
	for _, n := range []string{"a", "b", "c", "d"} {
		es = append(es, entry{h: tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644}})
	}
	err := unpack(tarOf(t, es...), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "more than 3 entries") {
		t.Fatalf("err = %v", err)
	}
	if err := unpack(tarOf(t, es[:3]...), t.TempDir()); err != nil {
		t.Fatalf("3 entries refused: %v", err)
	}
}

func TestUnpackStopsAtTheSizeLimit(t *testing.T) {
	old := maxUnpackBytes
	maxUnpackBytes = 8
	t.Cleanup(func() { maxUnpackBytes = old })
	err := unpack(tarOf(t, entry{h: tar.Header{Name: "big", Typeflag: tar.TypeReg, Mode: 0o644}, body: strings.Repeat("x", 100)}), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "larger than 8 bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestImageNeedsAReference(t *testing.T) {
	b := &Backend{opts: Options{CacheDir: t.TempDir()}}
	for _, ref := range []string{"", "debian:trixie-slim", "oci-layout:/some/dir"} {
		if _, err := b.image(context.Background(), ref); err == nil {
			t.Errorf("%q was accepted", ref)
		}
	}
}
