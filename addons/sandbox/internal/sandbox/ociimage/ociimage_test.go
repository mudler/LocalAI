package ociimage

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func testLayer(t *testing.T, files map[string]string) v1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
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

func testImage(t *testing.T, files map[string]string) v1.Image {
	t.Helper()
	img, err := mutate.AppendLayers(empty.Image, testLayer(t, files))
	if err != nil {
		t.Fatal(err)
	}
	img, err = mutate.Config(img, v1.Config{Env: []string{"PATH=/usr/bin:/bin", "FROM_IMAGE=1"}})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// writeLayout stores img in an OCI layout folder and returns the folder and
// the digest of the image.
func writeLayout(t *testing.T, img v1.Image) (string, v1.Hash) {
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

func refOf(dir string, d v1.Hash) string { return LayoutPrefix + dir + "@" + d.String() }

func TestResolveRefusals(t *testing.T) {
	for _, ref := range []string{"", "debian:trixie-slim", "example.com/guest:latest", LayoutPrefix + "/some/dir", LayoutPrefix + "/some/dir@nothex"} {
		if _, _, err := Resolve(context.Background(), ref); err == nil {
			t.Errorf("%q was accepted", ref)
		}
	}
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	got, load, err := Resolve(context.Background(), refOf(dir, d))
	if err != nil || got != d.String() {
		t.Fatalf("Resolve = %q, %v", got, err)
	}
	if _, err := load(); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutRefusesSwappedManifest(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	other := testImage(t, map[string]string{"b": "y"})
	p, err := layout.FromPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WriteImage(other); err != nil {
		t.Fatal(err)
	}
	raw, err := other.RawManifest()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BlobPath(dir, d), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	_, _, err = Prepare(context.Background(), cache, refOf(dir, d), func(v1.Image, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("a swapped manifest: %v", err)
	}
	assertNoCacheEntry(t, cache)
}

func TestLayoutRefusesSwappedLayer(t *testing.T) {
	img := testImage(t, map[string]string{"a": "x"})
	m, err := img.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	dir, d := writeLayout(t, img)
	if err := os.WriteFile(BlobPath(dir, m.Layers[0].Digest), []byte("swapped layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	_, _, err = Prepare(context.Background(), cache, refOf(dir, d), func(v1.Image, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("a swapped layer: %v", err)
	}
	assertNoCacheEntry(t, cache)
}

// assertNoCacheEntry checks that no folder in cache is complete: env.json is
// written last, so its absence is the mark of a refused build.
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

func TestPrepareBuildsOnceAndReturnsEnv(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := t.TempDir()
	var builds atomic.Int32
	build := func(img v1.Image, out string) error {
		builds.Add(1)
		rc := Extract(img)
		defer rc.Close()
		tr := tar.NewReader(rc)
		h, err := tr.Next()
		if err != nil || h.Name != "a" {
			t.Errorf("first entry = %v, %v", h, err)
		}
		return os.WriteFile(filepath.Join(out, "marker"), []byte("built"), 0o600)
	}
	var wg sync.WaitGroup
	envs := make([][]string, 6)
	for i := range envs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, env, err := Prepare(context.Background(), cache, refOf(dir, d), build)
			if err != nil {
				t.Error(err)
				return
			}
			if filepath.Base(out) != Key(d.String()) {
				t.Errorf("folder %q", out)
			}
			envs[i] = env
		}()
	}
	wg.Wait()
	if builds.Load() != 1 {
		t.Fatalf("built %d times", builds.Load())
	}
	for _, env := range envs {
		if strings.Join(env, " ") != "PATH=/usr/bin:/bin FROM_IMAGE=1" {
			t.Fatalf("env = %v", env)
		}
	}
}

func TestPrepareRebuildsAnIncompleteFolder(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := t.TempDir()
	stale := filepath.Join(cache, Key(d.String()))
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "leftover"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(_ v1.Image, out string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "leftover")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leftover of an unfinished build is still there: %v", err)
	}
	if _, err := ReadEnv(out); err != nil {
		t.Fatalf("the rebuilt folder is not complete: %v", err)
	}
}

func TestPrepareCreatesAMissingCacheFolder(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := filepath.Join(t.TempDir(), "not", "there", "yet")
	if _, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(_ v1.Image, out string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRefusesABuildThatWritesEnvJSON(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := t.TempDir()
	_, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(_ v1.Image, out string) error {
		return os.WriteFile(filepath.Join(out, "env.json"), []byte("[]"), 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "env.json") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ReadEnv(filepath.Join(cache, Key(d.String()))); err == nil {
		t.Fatal("the folder of the refused build is marked complete")
	}
}

// A caller that waits for the lock reads the env.json that the first caller
// wrote, and does not build.
func TestPrepareWaiterUsesTheFolderThatTheLockHolderBuilt(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := t.TempDir()
	unlock, err := LockFile(context.Background(), filepath.Join(cache, Key(d.String())+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	var builds atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(_ v1.Image, out string) error {
			builds.Add(1)
			return nil
		})
		done <- err
	}()
	// The holder finishes the folder while the waiter polls the lock.
	folder := filepath.Join(cache, Key(d.String()))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "env.json"), []byte(`["A=1"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 0 {
		t.Fatal("the waiter built the folder again")
	}
}

func TestPrepareRebuildsOverReadOnlyFolders(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := t.TempDir()
	stale := filepath.Join(cache, Key(d.String()))
	sub := filepath.Join(stale, "rootfs", "ro", "deeper")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sub, filepath.Join(stale, "rootfs", "ro")} {
		if err := os.Chmod(p, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = RemoveTree(cache) })
	// A failing build that makes read-only folders must not block the retry.
	failing := func(_ v1.Image, out string) error {
		p := filepath.Join(out, "rootfs", "ro")
		if err := os.MkdirAll(p, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(p, 0o555); err != nil {
			return err
		}
		return errors.New("stop")
	}
	if _, _, err := Prepare(context.Background(), cache, refOf(dir, d), failing); err == nil {
		t.Fatal("the failing build worked")
	}
	if _, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(_ v1.Image, out string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareDoesNotMarkAFailedBuild(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := t.TempDir()
	_, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(v1.Image, string) error { return errors.New("disk full") })
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	assertNoCacheEntry(t, cache)
}

func TestCheckEntryName(t *testing.T) {
	for _, bad := range []string{"/etc/passwd", "../x", "a/../../x", "./../x", ".."} {
		if CheckEntryName(bad) == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, ok := range []string{"a", "./a/b", "a/../b", "opt/attrs/"} {
		if err := CheckEntryName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

func TestPrepareTarGivesTheFlattenedTar(t *testing.T) {
	dir, d := writeLayout(t, testImage(t, map[string]string{"a": "x"}))
	cache := t.TempDir()
	var names []string
	out, env, err := PrepareTar(context.Background(), cache, refOf(dir, d), func(r io.Reader, folder string) error {
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			names = append(names, h.Name)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "a" || len(env) != 2 || filepath.Base(out) != Key(d.String()) {
		t.Fatalf("names %v, env %v, folder %s", names, env, out)
	}
}
