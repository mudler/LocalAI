// certificateIdentity is unexported, so its tests live in package
// cosignverify; the external suite's RunSpecs picks them up.
package cosignverify

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
)

const (
	testIssuer  = "https://token.actions.githubusercontent.com"
	sharedSAN   = "https://github.com/example/signer/.github/workflows/release.yml@refs/tags/v1.0.0"
	callerRepo  = "https://github.com/acme/gallery"
	otherCaller = "https://github.com/acme/other"
)

func summary(san, repo string) certificate.Summary {
	return certificate.Summary{
		SubjectAlternativeName: san,
		Extensions:             certificate.Extensions{Issuer: testIssuer, SourceRepositoryURI: repo},
	}
}

var _ = Describe("certificateIdentity", func() {
	shared := Policy{Issuer: testIssuer, IdentityRegex: `^https://github\.com/example/signer/\.github/workflows/release\.yml@refs/tags/.+$`}

	It("builds the short identity when no source repository is set", func() {
		id, err := shared.certificateIdentity()
		Expect(err).NotTo(HaveOccurred())
		// Without the field, any caller of the shared workflow matches,
		// which is today's behaviour and must stay so.
		Expect(id.Verify(summary(sharedSAN, callerRepo))).To(Succeed())
		Expect(id.Verify(summary(sharedSAN, otherCaller))).To(Succeed())
	})

	It("pins the caller when a source repository is set", func() {
		p := shared
		p.SourceRepository = callerRepo
		id, err := p.certificateIdentity()
		Expect(err).NotTo(HaveOccurred())
		Expect(id.Verify(summary(sharedSAN, callerRepo))).To(Succeed())
		Expect(id.Verify(summary(sharedSAN, otherCaller))).NotTo(Succeed())
	})

	It("compares the source repository exactly", func() {
		p := shared
		p.SourceRepository = callerRepo
		id, err := p.certificateIdentity()
		Expect(err).NotTo(HaveOccurred())
		for _, near := range []string{callerRepo + "/", callerRepo + ".git", "https://github.com/ACME/gallery"} {
			Expect(id.Verify(summary(sharedSAN, near))).NotTo(Succeed(), near)
		}
	})

	It("still checks the SAN and the issuer", func() {
		p := shared
		p.SourceRepository = callerRepo
		id, err := p.certificateIdentity()
		Expect(err).NotTo(HaveOccurred())
		Expect(id.Verify(summary("https://github.com/acme/gallery/.github/workflows/evil.yml@refs/heads/main", callerRepo))).NotTo(Succeed())
	})
})
