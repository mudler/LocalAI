package main

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOcipatch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox ocipatch Suite")
}

func mustLayer() v1.Layer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	Expect(tw.WriteHeader(&tar.Header{Name: "etc/os", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2})).To(Succeed())
	_, _ = tw.Write([]byte("ok"))
	_ = tw.Close()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil })
	Expect(err).NotTo(HaveOccurred())
	return l
}

var _ = Describe("ocipatch", func() {
	It("adds a file on top", func() {
		base := filepath.Join(GinkgoT().TempDir(), "base")
		p, err := layout.Write(base, empty.Index)
		Expect(err).NotTo(HaveOccurred())
		img, err := mutate.AppendLayers(empty.Image, mustLayer())
		Expect(err).NotTo(HaveOccurred())
		Expect(p.AppendImage(img)).To(Succeed())
		src := filepath.Join(GinkgoT().TempDir(), "agent")
		Expect(os.WriteFile(src, []byte("new agent"), 0o755)).To(Succeed())
		out := filepath.Join(GinkgoT().TempDir(), "out")
		ref, err := patch(base, out, []string{"usr/sbin/agent=" + src})
		Expect(err).NotTo(HaveOccurred())
		var found string
		_, _, err = ociimage.PrepareTar(context.Background(), GinkgoT().TempDir(), ref, func(r io.Reader, _ string) error {
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
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(Equal("new agent"))
		fresh := func() string { return filepath.Join(GinkgoT().TempDir(), "out") }
		for _, bad := range []string{"/abs=x", "../up=x", "a/../b=x", "a//b=x", "./a=x", "..=x"} {
			_, err := patch(base, fresh(), []string{bad})
			Expect(err).To(MatchError(ContainSubstring("-add")), "-add %q", bad)
		}
		// An existing -out, or -out equal to -in, is refused: a second manifest
		// would be appended to the existing layout.
		_, err = patch(base, out, []string{"usr/sbin/agent=" + src})
		Expect(err).To(MatchError(ContainSubstring("exists")), "existing -out")
		_, err = patch(base, base, []string{"usr/sbin/agent=" + src})
		Expect(err).To(MatchError(ContainSubstring("same as -in")), "-out equal to -in")
	})

	// The patched image keeps the platform of the source descriptor, and an
	// image without one stays without one (no platform is invented).
	It("keeps the source platform", func() {
		for _, want := range []*v1.Platform{
			{OS: "linux", Architecture: "arm64"},
			nil,
		} {
			base := filepath.Join(GinkgoT().TempDir(), "base")
			p, err := layout.Write(base, empty.Index)
			Expect(err).NotTo(HaveOccurred())
			img, err := mutate.AppendLayers(empty.Image, mustLayer())
			Expect(err).NotTo(HaveOccurred())
			var opts []layout.Option
			if want != nil {
				opts = append(opts, layout.WithPlatform(*want))
			}
			Expect(p.AppendImage(img, opts...)).To(Succeed())
			src := filepath.Join(GinkgoT().TempDir(), "agent")
			Expect(os.WriteFile(src, []byte("new agent"), 0o755)).To(Succeed())
			out := filepath.Join(GinkgoT().TempDir(), "out")
			_, err = patch(base, out, []string{"usr/sbin/agent=" + src})
			Expect(err).NotTo(HaveOccurred())
			idx, err := layout.ImageIndexFromPath(out)
			Expect(err).NotTo(HaveOccurred())
			m, err := idx.IndexManifest()
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Manifests).To(HaveLen(1))
			got := m.Manifests[0].Platform
			if want == nil {
				Expect(got).To(BeNil(), "platform invented")
			} else {
				Expect(got).NotTo(BeNil())
				Expect(got.OS).To(Equal(want.OS))
				Expect(got.Architecture).To(Equal(want.Architecture))
			}
		}
	})
})
