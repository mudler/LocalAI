// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pin, the one place a mutable entry becomes a digest", func() {
	const resolved = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	mutable := Release{ID: "demo", Version: "0.0.0-development", Artifact: "oci://quay.example/acme/addons:demo-development", Mutable: true, OS: "linux", Arch: "amd64"}

	It("resolves the tag at install and builds the version from the digest", func() {
		var asked string
		p, err := Pin(context.Background(), mutable, func(_ context.Context, image, platform string) (string, error) {
			asked = image + " " + platform
			return resolved, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(asked).To(Equal("quay.example/acme/addons:demo-development linux/amd64"))
		Expect(p.Digest).To(Equal(resolved))
		Expect(p.Version).To(Equal("0.0.0-development.cccccccccccc"))
		Expect(p.ByDigest()).To(Equal("quay.example/acme/addons@" + resolved))
	})
	It("refuses a tag that cannot be resolved or a answer that is not a digest", func() {
		_, err := Pin(context.Background(), mutable, func(context.Context, string, string) (string, error) { return "", errors.New("down") })
		Expect(err).To(HaveOccurred())
		_, err = Pin(context.Background(), mutable, func(context.Context, string, string) (string, error) { return "latest", nil })
		Expect(err).To(HaveOccurred())
	})
	It("keeps a pinned entry as it is and never looks it up", func() {
		r := mutable
		r.Mutable, r.Digest = false, resolved
		p, err := Pin(context.Background(), r, func(context.Context, string, string) (string, error) {
			Fail("a pinned entry needs no lookup")
			return "", nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Digest).To(Equal(resolved))
		Expect(p.Version).To(Equal(r.Version))
	})
	It("refuses an entry that is neither mutable nor pinned, and a mutable one that has a digest", func() {
		r := mutable
		r.Mutable = false
		_, err := Pin(context.Background(), r, nil)
		Expect(err).To(HaveOccurred())
		r = mutable
		r.Digest = resolved
		_, err = Pin(context.Background(), r, nil)
		Expect(err).To(HaveOccurred())
	})
})
