package ociimage

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Progress, Cached and Forget", func() {
	// serve pushes img to an in-memory registry and returns its reference by
	// digest.
	serve := func(img v1.Image) string {
		srv := httptest.NewServer(registry.New())
		DeferCleanup(srv.Close)
		host := strings.TrimPrefix(srv.URL, "http://")
		tag, err := name.NewTag(host + "/guest/test:v1")
		Expect(err).NotTo(HaveOccurred())
		Expect(remote.Write(tag, img)).To(Succeed())
		d, err := img.Digest()
		Expect(err).NotTo(HaveOccurred())
		return host + "/guest/test@" + d.String()
	}

	It("counts the bytes of a pull from a registry up to the size of the layers", func() {
		img := testImage(map[string]string{"etc/hello": strings.Repeat("hello ", 5000)})
		ref := serve(img)
		p := &Progress{}
		Expect(p.Fraction()).To(BeZero())
		cache := GinkgoT().TempDir()
		_, _, err := PrepareTar(WithProgress(context.Background(), p), cache, ref, func(tarStream io.Reader, dir string) error {
			_, err := io.Copy(io.Discard, tarStream)
			return err
		})
		Expect(err).NotTo(HaveOccurred())
		done, total := p.Bytes()
		Expect(total).To(BeNumerically(">", 0))
		Expect(done).To(BeNumerically(">=", total), "the layers were read in full")
		Expect(p.Fraction()).To(Equal(1.0))
	})

	It("tells a cached image from one that is not, and forgets it", func() {
		img := testImage(map[string]string{"a": "b"})
		dir, d := writeLayout(img)
		ref := refOf(dir, d)
		cache := GinkgoT().TempDir()
		ctx := context.Background()
		Expect(Cached(ctx, cache, ref)).To(BeFalse())
		_, _, err := Prepare(ctx, cache, ref, noBuild)
		Expect(err).NotTo(HaveOccurred())
		Expect(Cached(ctx, cache, ref)).To(BeTrue())
		Expect(Forget(ctx, cache, ref)).To(Succeed())
		Expect(Cached(ctx, cache, ref)).To(BeFalse())
		Expect(Forget(ctx, cache, ref)).To(Succeed(), "forgetting what is gone is not an error")
		Expect(Cached(ctx, cache, "not-a-digest-reference")).To(BeFalse())
	})
})
