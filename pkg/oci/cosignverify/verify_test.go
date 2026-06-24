package cosignverify_test

import (
	"context"
	"os"
	"time"

	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Policy", func() {
	It("rejects an empty policy", func() {
		_, err := cosignverify.NewVerifier(cosignverify.Policy{}, nil, nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects a policy missing the identity", func() {
		_, err := cosignverify.NewVerifier(cosignverify.Policy{
			Issuer: "https://token.actions.githubusercontent.com",
		}, nil, nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects a policy missing the issuer", func() {
		_, err := cosignverify.NewVerifier(cosignverify.Policy{
			IdentityRegex: "^https://github.com/example/.*",
		}, nil, nil)
		Expect(err).To(HaveOccurred())
	})

	It("constructs a verifier given a complete policy", func() {
		v, err := cosignverify.NewVerifier(cosignverify.Policy{
			Issuer:        "https://token.actions.githubusercontent.com",
			IdentityRegex: `^https://github.com/example/.*`,
		}, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(v).NotTo(BeNil())
	})
})

// Live tests hit the public Sigstore TUF mirror, the source registry, and
// (for positive cases) the Rekor log. Too flaky for the default suite —
// gate on LOCALAI_COSIGN_LIVE=1.
var _ = Describe("VerifyImage", func() {
	BeforeEach(func() {
		if os.Getenv("LOCALAI_COSIGN_LIVE") == "" {
			Skip("set LOCALAI_COSIGN_LIVE=1 to run live cosign verification")
		}
	})

	It("rejects an image without a Sigstore bundle referrer", func() {
		v, err := cosignverify.NewVerifier(cosignverify.Policy{
			Issuer:        "https://token.actions.githubusercontent.com",
			IdentityRegex: `^https://github\.com/example/.*`,
		}, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// alpine:latest is unsigned; the referrers API returns an empty
		// (or 404 → empty) index, so we should see "no referrers" or
		// "no bundle referrer" rather than a hard parse error.
		err = v.VerifyImage(ctx, "alpine:latest")
		Expect(err).To(HaveOccurred())
	})

	// End-to-end positive test. Requires:
	//   LOCALAI_COSIGN_LIVE=1
	//   LOCALAI_COSIGN_LIVE_IMAGE=<image-ref-signed-with-new-bundle-format>
	//   LOCALAI_COSIGN_LIVE_ISSUER=<expected OIDC issuer>
	//   LOCALAI_COSIGN_LIVE_IDENTITY_REGEX=<expected identity SAN regex>
	//
	// No defaults — we don't have a stable third-party image known to be
	// signed in the new-bundle-format yet. Once the local-ai-backends CI
	// is signing images, plug one of those refs in here.
	It("verifies a signed image when LOCALAI_COSIGN_LIVE_IMAGE is set", func() {
		image := os.Getenv("LOCALAI_COSIGN_LIVE_IMAGE")
		issuer := os.Getenv("LOCALAI_COSIGN_LIVE_ISSUER")
		identityRegex := os.Getenv("LOCALAI_COSIGN_LIVE_IDENTITY_REGEX")
		if image == "" || issuer == "" || identityRegex == "" {
			Skip("set LOCALAI_COSIGN_LIVE_IMAGE / _ISSUER / _IDENTITY_REGEX to run the positive case")
		}

		v, err := cosignverify.NewVerifier(cosignverify.Policy{
			Issuer:        issuer,
			IdentityRegex: identityRegex,
		}, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		Expect(v.VerifyImage(ctx, image)).To(Succeed())
	})
})
