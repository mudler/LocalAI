package credentials_test

import (
	"errors"
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
)

var _ = Describe("AuthError", Serial, func() {
	cause := errors.New("upstream said no")

	It("says no rule matches when none does", func() {
		useStore("")
		err := credentials.NewAuthError("https://ghcr.io/acme/img", "ghcr.io/acme/img:latest", 401, cause)
		Expect(err).To(MatchError(ContainSubstring("no credentials rule matches")))
		Expect(err).To(MatchError(ContainSubstring("ghcr.io/acme/img:latest")))
		var authErr *credentials.AuthError
		Expect(errors.As(err, &authErr)).To(BeTrue())
		Expect(authErr.Match).To(BeEmpty())
		Expect(errors.Is(err, cause)).To(BeTrue())
	})

	It("names the rule the server rejected", func() {
		useStore("- match: ghcr.io/acme\n  bearer: wrong\n")
		err := credentials.NewAuthError("https://ghcr.io/acme/img", "ghcr.io/acme/img:latest", 403, cause)
		Expect(err).To(MatchError(ContainSubstring(`credential "ghcr.io/acme" was rejected`)))
		Expect(err.Error()).NotTo(ContainSubstring("wrong"))
	})

	It("keeps signed query strings out of HTTP errors", func() {
		useStore("")
		u, err := url.Parse("https://cdn.example.com/blob?X-Amz-Signature=topsecret")
		Expect(err).NotTo(HaveOccurred())
		Expect(credentials.HTTPAuthError(u, 401, cause).Error()).NotTo(ContainSubstring("topsecret"))
	})
})
