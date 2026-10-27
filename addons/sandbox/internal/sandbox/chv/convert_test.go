package chv

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// attrLayer is a layer with every file kind the conversion must keep: a
// setuid file, a symlink, a hard link, a fifo, a character device and a file
// with an xattr. It also holds a kernel when withKernel is true.
func attrLayer(t *testing.T, withKernel bool) v1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	add(&tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "opt/attrs/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "opt/attrs/suid", Typeflag: tar.TypeReg, Mode: 0o4755}, "#!/bin/sh\n")
	add(&tar.Header{Name: "opt/attrs/link", Typeflag: tar.TypeSymlink, Linkname: "suid", Mode: 0o777}, "")
	add(&tar.Header{Name: "opt/attrs/hard", Typeflag: tar.TypeLink, Linkname: "opt/attrs/suid"}, "")
	add(&tar.Header{Name: "opt/attrs/fifo", Typeflag: tar.TypeFifo, Mode: 0o600, Uid: 1000, Gid: 1000}, "")
	add(&tar.Header{Name: "opt/attrs/null", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 3}, "")
	add(&tar.Header{Name: "opt/attrs/xattr", Typeflag: tar.TypeReg, Mode: 0o644, PAXRecords: map[string]string{"SCHILY.xattr.user.demo": "hello"}}, "x\n")
	if withKernel {
		add(&tar.Header{Name: "boot/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
		add(&tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeReg, Mode: 0o644}, "not a real kernel")
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// writeLayout stores img in an OCI layout folder and returns its reference.
func writeLayout(t *testing.T, img v1.Image) string {
	t.Helper()
	dir := t.TempDir()
	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AppendImage(img); err != nil {
		t.Fatal(err)
	}
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return layoutPrefix + dir + "@" + d.String()
}

func testImage(t *testing.T, withKernel bool) v1.Image {
	t.Helper()
	img, err := mutate.AppendLayers(empty.Image, attrLayer(t, withKernel))
	if err != nil {
		t.Fatal(err)
	}
	img, err = mutate.Config(img, v1.Config{Env: []string{"PATH=/usr/bin:/bin", "FROM_IMAGE=1"}})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func debugfsOut(t *testing.T, img, cmd string) string {
	t.Helper()
	out, err := exec.Command("debugfs", "-c", "-R", cmd, img).Output()
	if err != nil {
		t.Fatalf("debugfs %s: %v", cmd, err)
	}
	return string(out)
}

func TestConvertKeepsFileKinds(t *testing.T) {
	needTools(t, "debugfs")
	b := &Backend{opts: Options{CacheDir: t.TempDir()}}
	ref := writeLayout(t, testImage(t, true))
	gi, err := b.image(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gi.Env, " ") != "PATH=/usr/bin:/bin FROM_IMAGE=1" {
		t.Fatalf("env = %v", gi.Env)
	}
	if k, _ := os.ReadFile(gi.Kernel); string(k) != "not a real kernel" {
		t.Fatalf("kernel = %q", k)
	}
	ls := debugfsOut(t, gi.Root, "ls -p /opt/attrs")
	ents, _, err := parseLS([]byte(ls))
	if err != nil {
		t.Fatalf("%v\n%s", err, ls)
	}
	byName := map[string]lsEntry{}
	for _, e := range ents {
		byName[e.Name] = e
	}
	for name, mode := range map[string]uint32{"suid": 0o104755, "hard": 0o104755, "link": 0o120777, "fifo": 0o010600, "null": 0o020666, "xattr": 0o100644} {
		if byName[name].Mode != mode {
			t.Errorf("%s mode = %o, want %o", name, byName[name].Mode, mode)
		}
	}
	if byName["suid"].Ino != byName["hard"].Ino {
		t.Error("the hard link is a copy")
	}
	if !strings.Contains(ls, "/1000/1000/fifo/") || !strings.Contains(ls, "/0/0/suid/") {
		t.Errorf("owners lost:\n%s", ls)
	}
	if st := debugfsOut(t, gi.Root, "stat /opt/attrs/null"); !strings.Contains(st, "Device major/minor number: 01:03") {
		t.Errorf("device:\n%s", st)
	}
	ea := debugfsOut(t, gi.Root, "ea_get -x /opt/attrs/xattr user.demo")
	if v, _, err := parseEAGet("user.demo")([]byte(ea)); err != nil || string(v) != "hello" {
		t.Errorf("xattr = %q, %v", ea, err)
	}
	// A second call uses the cache.
	before, _ := os.Stat(gi.Root)
	gi2, err := b.image(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(gi2.Root)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("the image was converted again")
	}
}

func TestConvertRefusals(t *testing.T) {
	b := &Backend{opts: Options{CacheDir: t.TempDir()}}
	if _, err := b.image(context.Background(), writeLayout(t, testImage(t, false))); err == nil || !strings.Contains(err.Error(), "/boot/vmlinux") {
		t.Fatalf("no kernel: %v", err)
	}
	for _, ref := range []string{"debian:trixie-slim", "example.com/guest:latest", layoutPrefix + "/some/dir", ""} {
		if _, err := b.image(context.Background(), ref); err == nil {
			t.Fatalf("%q was accepted", ref)
		}
	}
	entries, _ := os.ReadDir(b.opts.CacheDir)
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(b.opts.CacheDir, e.Name(), "env.json")); err == nil {
				t.Fatalf("a failed conversion left a complete cache entry: %s", e.Name())
			}
		}
	}
}

// A hostile layer must not make the host write an unbounded kernel.
func TestConvertRefusesHugeKernel(t *testing.T) {
	old := maxKernelBytes
	maxKernelBytes = 8
	t.Cleanup(func() { maxKernelBytes = old })
	b := &Backend{opts: Options{CacheDir: t.TempDir()}}
	_, err := b.image(context.Background(), writeLayout(t, testImage(t, true)))
	if err == nil || !strings.Contains(err.Error(), "larger than 8 bytes") {
		t.Fatalf("a kernel over the limit: %v", err)
	}
	entries, _ := os.ReadDir(b.opts.CacheDir)
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(b.opts.CacheDir, e.Name(), "env.json")); err == nil {
			t.Fatalf("a refused kernel left a complete cache entry: %s", e.Name())
		}
	}
}

// tarImage is an image with one layer made of the entries fill writes.
func tarImage(t *testing.T, fill func(tw *tar.Writer)) v1.Image {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	fill(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(empty.Image, l)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func tarEntry(tw *tar.Writer, h *tar.Header, body string) {
	h.Size = int64(len(body))
	if h.Mode == 0 {
		h.Mode = 0o644
	}
	if err := tw.WriteHeader(h); err != nil {
		panic(err)
	}
	io.WriteString(tw, body)
}

// writeLayoutDir is writeLayout that also returns the folder and the digest.
func writeLayoutDir(t *testing.T, img v1.Image) (string, v1.Hash) {
	t.Helper()
	dir := t.TempDir()
	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AppendImage(img); err != nil {
		t.Fatal(err)
	}
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return dir, d
}

func TestLayoutRefusesSwappedManifest(t *testing.T) {
	dir, d := writeLayoutDir(t, testImage(t, true))
	other := testImage(t, false)
	p, err := layout.FromPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WriteImage(other); err != nil { // the other image's blobs, so only the digest is wrong
		t.Fatal(err)
	}
	raw, err := other.RawManifest()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath(dir, d), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Backend{opts: Options{CacheDir: t.TempDir()}}
	_, err = b.image(context.Background(), layoutPrefix+dir+"@"+d.String())
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("a swapped manifest: %v", err)
	}
	assertNoCacheEntry(t, b.opts.CacheDir)
}

func TestLayoutRefusesSwappedLayer(t *testing.T) {
	img := testImage(t, true)
	m, err := img.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	dir, d := writeLayoutDir(t, img)
	if err := os.WriteFile(blobPath(dir, m.Layers[0].Digest), []byte("swapped layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Backend{opts: Options{CacheDir: t.TempDir()}}
	_, err = b.image(context.Background(), layoutPrefix+dir+"@"+d.String())
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("a swapped layer: %v", err)
	}
	assertNoCacheEntry(t, b.opts.CacheDir)
}

// assertNoCacheEntry checks that no folder in cache is complete: env.json is
// written last, so its absence is the mark of a refused conversion.
func assertNoCacheEntry(t *testing.T, cache string) {
	t.Helper()
	entries, _ := os.ReadDir(cache)
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(cache, e.Name(), "env.json")); err == nil {
				t.Fatalf("a refused image left a complete cache entry: %s", e.Name())
			}
		}
	}
}

// Hostile tar entries must not be converted or written outside the image.
func TestConvertRefusesHostileTarPaths(t *testing.T) {
	cases := map[string]func(tw *tar.Writer){
		"dot-dot-escape": func(tw *tar.Writer) {
			tarEntry(tw, &tar.Header{Name: "../evil", Typeflag: tar.TypeReg}, "x")
			tarEntry(tw, &tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeReg}, "K")
		},
		"absolute-escape": func(tw *tar.Writer) {
			tarEntry(tw, &tar.Header{Name: "/abs/evil", Typeflag: tar.TypeReg}, "x")
			tarEntry(tw, &tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeReg}, "K")
		},
		"kernel-symlink": func(tw *tar.Writer) {
			tarEntry(tw, &tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, "")
		},
	}
	for name, fill := range cases {
		t.Run(name, func(t *testing.T) {
			b := &Backend{opts: Options{CacheDir: t.TempDir()}}
			_, err := b.image(context.Background(), writeLayout(t, tarImage(t, fill)))
			if err == nil {
				t.Fatal("the hostile image was converted")
			}
			assertNoCacheEntry(t, b.opts.CacheDir)
		})
	}
}

// Two converters of one digest share one conversion and one cache folder.
func TestConvertConcurrentSameDigest(t *testing.T) {
	b := &Backend{opts: Options{CacheDir: t.TempDir()}}
	ref := writeLayout(t, testImage(t, true))
	var wg sync.WaitGroup
	roots := make([]string, 2)
	errs := make([]error, 2)
	for i := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gi, err := b.image(context.Background(), ref)
			roots[i], errs[i] = gi.Root, err
		}()
	}
	wg.Wait()
	for i := range roots {
		if errs[i] != nil {
			t.Fatalf("converter %d: %v", i, errs[i])
		}
	}
	if roots[0] != roots[1] {
		t.Fatalf("two folders: %v", roots)
	}
	entries, _ := os.ReadDir(b.opts.CacheDir)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		t.Fatalf("cache folders: %v", dirs)
	}
	folder, _ := os.ReadDir(filepath.Join(b.opts.CacheDir, dirs[0]))
	for _, e := range folder {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a temporary file was left: %s", e.Name())
		}
	}
}
