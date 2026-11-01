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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOciimage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox OCI Image Suite")
}

func testLayer(files map[string]string) v1.Layer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		Expect(tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})).To(Succeed())
		_, err := io.WriteString(tw, body)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(tw.Close()).To(Succeed())
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
	Expect(err).NotTo(HaveOccurred())
	return l
}

func testImage(files map[string]string) v1.Image {
	img, err := mutate.AppendLayers(empty.Image, testLayer(files))
	Expect(err).NotTo(HaveOccurred())
	img, err = mutate.Config(img, v1.Config{Env: []string{"PATH=/usr/bin:/bin", "FROM_IMAGE=1"}})
	Expect(err).NotTo(HaveOccurred())
	return img
}

// writeLayout stores img in an OCI layout folder and returns the folder and
// the digest of the image.
func writeLayout(img v1.Image) (string, v1.Hash) {
	dir := GinkgoT().TempDir()
	p, err := layout.Write(dir, empty.Index)
	Expect(err).NotTo(HaveOccurred())
	Expect(p.AppendImage(img)).To(Succeed())
	d, err := img.Digest()
	Expect(err).NotTo(HaveOccurred())
	return dir, d
}

func refOf(dir string, d v1.Hash) string { return LayoutPrefix + dir + "@" + d.String() }

// assertNoCacheEntry checks that no folder in cache is complete: env.json is
// written last, so its absence is the mark of a refused build.
func assertNoCacheEntry(cache string) {
	entries, _ := os.ReadDir(cache)
	for _, e := range entries {
		if e.IsDir() {
			_, err := os.Stat(filepath.Join(cache, e.Name(), "env.json"))
			Expect(err).To(HaveOccurred(), "a refused image left a complete cache entry: %s", e.Name())
		}
	}
}

var noBuild = func(v1.Image, string) error { return nil }

var _ = Describe("OCI image", func() {
	It("refuses references that are not a pinned layout", func() {
		for _, ref := range []string{"", "debian:trixie-slim", "example.com/guest:latest", LayoutPrefix + "/some/dir", LayoutPrefix + "/some/dir@nothex"} {
			_, _, err := Resolve(context.Background(), ref)
			Expect(err).To(HaveOccurred(), "%q was accepted", ref)
		}
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		got, load, err := Resolve(context.Background(), refOf(dir, d))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(d.String()))
		_, err = load()
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a layout with a swapped manifest", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		other := testImage(map[string]string{"b": "y"})
		p, err := layout.FromPath(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.WriteImage(other)).To(Succeed())
		raw, err := other.RawManifest()
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(BlobPath(dir, d), raw, 0o600)).To(Succeed())
		cache := GinkgoT().TempDir()
		_, _, err = Prepare(context.Background(), cache, refOf(dir, d), noBuild)
		Expect(err).To(MatchError(ContainSubstring("digest mismatch")), "a swapped manifest")
		assertNoCacheEntry(cache)
	})

	It("refuses a layout with a swapped layer", func() {
		img := testImage(map[string]string{"a": "x"})
		m, err := img.Manifest()
		Expect(err).NotTo(HaveOccurred())
		dir, d := writeLayout(img)
		Expect(os.WriteFile(BlobPath(dir, m.Layers[0].Digest), []byte("swapped layer"), 0o600)).To(Succeed())
		cache := GinkgoT().TempDir()
		_, _, err = Prepare(context.Background(), cache, refOf(dir, d), noBuild)
		Expect(err).To(MatchError(ContainSubstring("digest mismatch")), "a swapped layer")
		assertNoCacheEntry(cache)
	})

	It("builds once for concurrent callers and returns the env", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := GinkgoT().TempDir()
		var builds atomic.Int32
		build := func(img v1.Image, out string) error {
			defer GinkgoRecover()
			builds.Add(1)
			rc := Extract(img)
			defer func() { _ = rc.Close() }()
			tr := tar.NewReader(rc)
			h, err := tr.Next()
			Expect(err).NotTo(HaveOccurred())
			Expect(h.Name).To(Equal("a"))
			return os.WriteFile(filepath.Join(out, "marker"), []byte("built"), 0o600)
		}
		var wg sync.WaitGroup
		envs := make([][]string, 6)
		for i := range envs {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				out, env, err := Prepare(context.Background(), cache, refOf(dir, d), build)
				Expect(err).NotTo(HaveOccurred())
				Expect(filepath.Base(out)).To(Equal(Key(d.String())))
				envs[i] = env
			}()
		}
		wg.Wait()
		Expect(builds.Load()).To(BeEquivalentTo(1))
		for _, env := range envs {
			Expect(strings.Join(env, " ")).To(Equal("PATH=/usr/bin:/bin FROM_IMAGE=1"))
		}
	})

	It("rebuilds an incomplete folder", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := GinkgoT().TempDir()
		stale := filepath.Join(cache, Key(d.String()))
		Expect(os.MkdirAll(stale, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(stale, "leftover"), []byte("x"), 0o600)).To(Succeed())
		out, _, err := Prepare(context.Background(), cache, refOf(dir, d), noBuild)
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(filepath.Join(out, "leftover"))
		Expect(err).To(MatchError(os.ErrNotExist), "the leftover of an unfinished build is still there")
		_, err = ReadEnv(out)
		Expect(err).NotTo(HaveOccurred(), "the rebuilt folder is not complete")
	})

	It("creates a missing cache folder", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := filepath.Join(GinkgoT().TempDir(), "not", "there", "yet")
		_, _, err := Prepare(context.Background(), cache, refOf(dir, d), noBuild)
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a build that writes env.json", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := GinkgoT().TempDir()
		_, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(_ v1.Image, out string) error {
			return os.WriteFile(filepath.Join(out, "env.json"), []byte("[]"), 0o600)
		})
		Expect(err).To(MatchError(ContainSubstring("env.json")))
		_, err = ReadEnv(filepath.Join(cache, Key(d.String())))
		Expect(err).To(HaveOccurred(), "the folder of the refused build is marked complete")
	})

	// A caller that waits for the lock reads the env.json that the first caller
	// wrote, and does not build.
	It("uses the folder that the lock holder built when it waits", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := GinkgoT().TempDir()
		unlock, err := LockFile(context.Background(), filepath.Join(cache, Key(d.String())+".lock"))
		Expect(err).NotTo(HaveOccurred())
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
		Expect(os.MkdirAll(folder, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(folder, "env.json"), []byte(`["A=1"]`), 0o600)).To(Succeed())
		unlock()
		Expect(<-done).To(Succeed())
		Expect(builds.Load()).To(BeZero(), "the waiter built the folder again")
	})

	It("rebuilds over read-only folders", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := GinkgoT().TempDir()
		stale := filepath.Join(cache, Key(d.String()))
		sub := filepath.Join(stale, "rootfs", "ro", "deeper")
		Expect(os.MkdirAll(sub, 0o700)).To(Succeed())
		for _, p := range []string{sub, filepath.Join(stale, "rootfs", "ro")} {
			Expect(os.Chmod(p, 0o555)).To(Succeed())
		}
		DeferCleanup(func() { _ = RemoveTree(cache) })
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
		_, _, err := Prepare(context.Background(), cache, refOf(dir, d), failing)
		Expect(err).To(HaveOccurred(), "the failing build worked")
		_, _, err = Prepare(context.Background(), cache, refOf(dir, d), noBuild)
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not mark a failed build as complete", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := GinkgoT().TempDir()
		_, _, err := Prepare(context.Background(), cache, refOf(dir, d), func(v1.Image, string) error { return errors.New("disk full") })
		Expect(err).To(MatchError(ContainSubstring("disk full")))
		assertNoCacheEntry(cache)
	})

	It("checks entry names", func() {
		for _, bad := range []string{"/etc/passwd", "../x", "a/../../x", "./../x", ".."} {
			Expect(CheckEntryName(bad)).NotTo(Succeed(), "%q was accepted", bad)
		}
		for _, ok := range []string{"a", "./a/b", "a/../b", "opt/attrs/"} {
			Expect(CheckEntryName(ok)).To(Succeed(), ok)
		}
	})

	It("gives the flattened tar to PrepareTar", func() {
		dir, d := writeLayout(testImage(map[string]string{"a": "x"}))
		cache := GinkgoT().TempDir()
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
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Join(names, ",")).To(Equal("a"))
		Expect(env).To(HaveLen(2))
		Expect(filepath.Base(out)).To(Equal(Key(d.String())))
	})
})
