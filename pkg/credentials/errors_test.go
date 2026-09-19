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

	It("keeps the cause so registry detail survives", func() {
		useStore("")
		err := credentials.NewAuthError("https://ghcr.io/acme/img", "ghcr.io/acme/img:latest", 401, cause)
		Expect(err).To(MatchError(HaveSuffix(": upstream said no")))
		useStore("- match: ghcr.io/acme\n  bearer: wrong\n")
		err = credentials.NewAuthError("https://ghcr.io/acme/img", "ghcr.io/acme/img:latest", 403, cause)
		Expect(err).To(MatchError(HaveSuffix(": upstream said no")))
	})

	It("says docker config was consulted too for a registry", func() {
		useStore("")
		err := credentials.NewRegistryAuthError("https://ghcr.io/acme/img", "ghcr.io/acme/img:latest", 401, cause)
		Expect(err).To(MatchError("authentication required for ghcr.io/acme/img:latest (status 401): no credentials rule matches it and docker config credentials, if any, were not accepted: upstream said no"))
	})

	It("does not consult the store when the caller chose the credential", func() {
		useStore("- match: https://cdn.example.com\n  bearer: store\n")
		u, err := url.Parse("https://cdn.example.com/blob")
		Expect(err).NotTo(HaveOccurred())
		authErr := credentials.HTTPProvidedCredentialError(u, 401, cause)
		Expect(authErr).To(MatchError(ContainSubstring("the provided credential was rejected by https://cdn.example.com/blob (status 401)")))
		Expect(authErr.Error()).NotTo(ContainSubstring("credentials rule"))
		Expect(errors.Is(authErr, cause)).To(BeTrue())
	})

	It("does not print an HTTP cause, which may quote the signed URL", func() {
		useStore("")
		u, err := url.Parse("https://cdn.example.com/blob?X-Amz-Signature=topsecret")
		Expect(err).NotTo(HaveOccurred())
		leaky := errors.New("failed to download url \"https://cdn.example.com/blob?X-Amz-Signature=topsecret\"")
		authErr := credentials.HTTPAuthError(u, 401, leaky)
		Expect(authErr.Error()).NotTo(ContainSubstring("topsecret"))
		Expect(errors.Is(authErr, leaky)).To(BeTrue())
	})

	It("keeps signed query strings out of HTTP errors", func() {
		useStore("")
		u, err := url.Parse("https://cdn.example.com/blob?X-Amz-Signature=topsecret")
		Expect(err).NotTo(HaveOccurred())
		Expect(credentials.HTTPAuthError(u, 401, cause).Error()).NotTo(ContainSubstring("topsecret"))
	})
})
