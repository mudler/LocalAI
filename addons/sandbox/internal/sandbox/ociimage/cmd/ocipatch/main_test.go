package main

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

func TestPatchAddsAFileOnTop(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base")
	p, err := layout.Write(base, empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(empty.Image, mustLayer(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AppendImage(img); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(src, []byte("new agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	ref, err := patch(base, out, []string{"usr/sbin/agent=" + src})
	if err != nil {
		t.Fatal(err)
	}
	var found string
	_, _, err = ociimage.PrepareTar(context.Background(), t.TempDir(), ref, func(r io.Reader, _ string) error {
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if h.Name == "usr/sbin/agent" {
				b, _ := io.ReadAll(tr)
				found = string(b)
			}
		}
	})
	if err != nil || found != "new agent" {
		t.Fatalf("found %q, err %v", found, err)
	}
	fresh := func() string { return filepath.Join(t.TempDir(), "out") }
	for _, bad := range []string{"/abs=x", "../up=x", "a/../b=x", "a//b=x", "./a=x", "..=x"} {
		_, err := patch(base, fresh(), []string{bad})
		if err == nil || !strings.Contains(err.Error(), "-add") {
			t.Errorf("-add %q: err = %v", bad, err)
		}
	}
	// An existing -out, or -out equal to -in, is refused: a second manifest
	// would be appended to the existing layout.
	if _, err := patch(base, out, []string{"usr/sbin/agent=" + src}); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("existing -out: err = %v", err)
	}
	if _, err := patch(base, base, []string{"usr/sbin/agent=" + src}); err == nil || !strings.Contains(err.Error(), "same as -in") {
		t.Errorf("-out equal to -in: err = %v", err)
	}
}

// The patched image keeps the platform of the source descriptor, and an
// image without one stays without one (no platform is invented).
func TestPatchKeepsTheSourcePlatform(t *testing.T) {
	for _, want := range []*v1.Platform{
		{OS: "linux", Architecture: "arm64"},
		nil,
	} {
		base := filepath.Join(t.TempDir(), "base")
		p, err := layout.Write(base, empty.Index)
		if err != nil {
			t.Fatal(err)
		}
		img, err := mutate.AppendLayers(empty.Image, mustLayer(t))
		if err != nil {
			t.Fatal(err)
		}
		var opts []layout.Option
		if want != nil {
			opts = append(opts, layout.WithPlatform(*want))
		}
		if err := p.AppendImage(img, opts...); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(t.TempDir(), "agent")
		if err := os.WriteFile(src, []byte("new agent"), 0o755); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "out")
		if _, err := patch(base, out, []string{"usr/sbin/agent=" + src}); err != nil {
			t.Fatal(err)
		}
		idx, err := layout.ImageIndexFromPath(out)
		if err != nil {
			t.Fatal(err)
		}
		m, err := idx.IndexManifest()
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Manifests) != 1 {
			t.Fatalf("want 1 manifest, got %d", len(m.Manifests))
		}
		got := m.Manifests[0].Platform
		switch {
		case want == nil && got != nil:
			t.Fatalf("platform invented: %+v", got)
		case want != nil && (got == nil || got.OS != want.OS || got.Architecture != want.Architecture):
			t.Fatalf("platform %+v, want %+v", got, want)
		}
	}
}

func mustLayer(t *testing.T) v1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "etc/os", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("ok"))
	tw.Close()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil })
	if err != nil {
		t.Fatal(err)
	}
	return l
}
