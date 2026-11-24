package gallery

import (
	"context"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// expireOCIGalleryCache makes every unpacked gallery artifact stale for the
// rest of the spec, so the next fetch goes back to the registry the way it
// would an hour later.
func expireOCIGalleryCache() {
	GinkgoHelper()
	original := ociGalleryCacheTTL
	ociGalleryCacheTTL = 0
	DeferCleanup(func() { ociGalleryCacheTTL = original })
}

var _ = Describe("oci:// gallery caches and the verification policy", func() {
	const index = "- name: acme-model\n"

	loose := &config.GalleryVerification{
		Issuer:        "https://token.actions.githubusercontent.com",
		IdentityRegex: "^https://github.com/acme/.*$",
	}
	tightened := &config.GalleryVerification{
		Issuer:           "https://token.actions.githubusercontent.com",
		IdentityRegex:    "^https://github.com/acme/.*$",
		SourceRepository: "https://github.com/acme/gallery",
	}

	BeforeEach(resetGalleryFailures)

	// countingVerifier stubs the signature check: it counts the calls and
	// refuses any policy the refuse func rejects.
	countingVerifier := func(refuse func(*config.GalleryVerification) bool) *atomic.Int64 {
		GinkgoHelper()
		var calls atomic.Int64
		stubGalleryVerifier(func(_ context.Context, p *config.GalleryVerification, _ string) error {
			calls.Add(1)
			if refuse(p) {
				return errNoGallerySignature
			}
			return nil
		})
		return &calls
	}

	It("fetches and verifies again when the policy changes", func() {
		srv, requests, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})
		calls := countingVerifier(func(*config.GalleryVerification) bool { return false })
		base := tempModelsDir()

		_, _, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "acme", Verification: loose}, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(calls.Load()).To(Equal(int64(1)))

		resetCounters(requests)
		body, _, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "acme", Verification: tightened}, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(index))
		Expect(calls.Load()).To(Equal(int64(2)), "content verified under the old policy was reused under the new one")
		Expect(requests.Load()).ToNot(BeZero(), "the registry was not contacted after the policy changed")
	})

	It("does not serve content verified under the old policy when the new one refuses it", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})
		countingVerifier(func(p *config.GalleryVerification) bool { return p.SourceRepository != "" })
		base := tempModelsDir()

		_, _, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "acme", Verification: loose}, base, false)
		Expect(err).ToNot(HaveOccurred())

		body, served, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "acme", Verification: tightened}, base, false)
		Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
		Expect(err.Error()).To(ContainSubstring("signature"))
	})

	It("does not fall back to the last known good copy after a verification failure", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})
		var refuse atomic.Bool
		countingVerifier(func(*config.GalleryVerification) bool { return refuse.Load() })
		base := tempModelsDir()
		g := config.Gallery{URL: url, Name: "acme", Verification: loose}

		_, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(galleryCachePath(base, url, loose)).To(BeARegularFile())

		// The publisher's artifact now fails the same policy, for example
		// because it was re-signed by an identity the policy does not trust.
		expireOCIGalleryCache()
		refuse.Store(true)

		body, served, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
		Expect(err.Error()).To(ContainSubstring("signature"))
	})

	It("does not serve an unverified copy once strict integrity is on", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})
		base := tempModelsDir()
		g := config.Gallery{URL: url, Name: "acme"}

		_, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())

		body, served, err := fetchGalleryIndex(context.Background(), g, base, true)
		Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
		Expect(err.Error()).To(ContainSubstring("verification"))
	})

	Context("when the registry is unreachable", func() {
		It("falls back to a copy verified under the same policy", func() {
			srv, _, _ := ociRegistry()
			url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
				{title: "index.yaml", body: index},
			})
			countingVerifier(func(*config.GalleryVerification) bool { return false })
			base := tempModelsDir()
			g := config.Gallery{URL: url, Name: "acme", Verification: loose}

			_, _, err := fetchGalleryIndex(context.Background(), g, base, false)
			Expect(err).ToNot(HaveOccurred())

			expireOCIGalleryCache()
			srv.Close()

			body, served, err := fetchGalleryIndex(context.Background(), g, base, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(Equal(index))
			Expect(served).To(Equal(galleryCachePath(base, url, loose)))
		})

		It("does not fall back to a copy verified under another policy", func() {
			srv, _, _ := ociRegistry()
			url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
				{title: "index.yaml", body: index},
			})
			countingVerifier(func(*config.GalleryVerification) bool { return false })
			base := tempModelsDir()

			_, _, err := fetchGalleryIndex(context.Background(),
				config.Gallery{URL: url, Name: "acme", Verification: loose}, base, false)
			Expect(err).ToNot(HaveOccurred())

			expireOCIGalleryCache()
			srv.Close()

			body, served, err := fetchGalleryIndex(context.Background(),
				config.Gallery{URL: url, Name: "acme", Verification: tightened}, base, false)
			Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
		})
	})
})
