package gallery

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// An invalid source_repository is refused by the policy's own validation, so
// seeing that refusal here proves the gallery field reaches the policy.
var _ = Describe("newGalleryVerifier", func() {
	It("passes the source repository to the policy", func() {
		_, err := newGalleryVerifier(&config.GalleryVerification{
			Issuer:           "https://token.actions.githubusercontent.com",
			IdentityRegex:    `^https://github\.com/example/.*$`,
			SourceRepository: "not-a-url",
		})
		Expect(err).To(MatchError(ContainSubstring("source repository")))
	})
})
