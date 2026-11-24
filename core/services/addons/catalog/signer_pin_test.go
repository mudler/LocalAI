// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	pinIssuer  = "https://issuer.example"
	pinPattern = `^https://github\.com/example-org/shared-workflows/\.github/workflows/sign\.yml@refs/tags/v.+$`
	pinRepo    = "https://github.com/example-org/example-repo"
)

const (
	ociLib   = "oci://registry.example/libs/fixture-addon:1"
	httpsLib = "https://registry.example/index.yaml"
)

var patternPinned = VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern, SourceRepository: pinRepo}

var _ = DescribeTable("signer policy of a source",
	func(kind Kind, url string, p VerificationPolicy, ok bool) {
		src := Source{ID: "lib", Name: "lib", URL: url, Enabled: true, Kind: kind, Policy: p}
		Expect(validSource(src)).To(Equal(ok))
	},
	Entry("oci:// add-on library: a pattern without a source repository", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern}, false),
	Entry("oci:// add-on library with no kind: a pattern without a source repository", Kind(""), ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern}, false),
	Entry("oci:// add-on library: a pattern with a source repository", KindAddon, ociLib, patternPinned, true),
	Entry("oci:// add-on library: an exact identity without a source repository", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, Identity: "signer@registry.example"}, true),
	Entry("oci:// add-on library: an exact identity with a source repository", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, Identity: "signer@registry.example", SourceRepository: pinRepo}, true),
	Entry("oci:// add-on library: a source repository without a required signature", KindAddon, ociLib, VerificationPolicy{Issuer: pinIssuer, Identity: "signer@registry.example", SourceRepository: pinRepo}, false),
	Entry("oci:// add-on library: a source repository over http", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern, SourceRepository: "http://github.com/example-org/example-repo"}, false),
	Entry("oci:// add-on library: a source repository that is not a URL", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern, SourceRepository: "example-org/example-repo"}, false),
	Entry("oci:// add-on library: a source repository with a space around it", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern, SourceRepository: " " + pinRepo}, false),
	Entry("oci:// add-on library: both an identity and a pattern", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, Identity: "x", IdentityRegex: pinPattern, SourceRepository: pinRepo}, false),
	Entry("oci:// add-on library: a pattern that is not anchored", KindAddon, ociLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: `https://x/.*`, SourceRepository: pinRepo}, false),
	// An https add-on library can also list backends, which verify against a
	// pattern alone; its add-ons are refused at install (see packages).
	Entry("https add-on library: a pattern without a source repository", KindAddon, httpsLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern}, true),
	Entry("https add-on library: a pattern with a source repository", KindAddon, httpsLib, patternPinned, true),
	Entry("backend library: a pattern without a source repository", KindBackend, httpsLib, VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern}, true),
	Entry("backend library: a pattern with a source repository", KindBackend, httpsLib, patternPinned, true),
	Entry("backend library: a source repository without a required signature", KindBackend, httpsLib, VerificationPolicy{SourceRepository: pinRepo}, false),
)

var _ = Describe("policy rules for an add-on", func() {
	It("accepts a pattern for an add-on only with a source repository", func() {
		Expect(patternPinned.ValidForAddon()).To(BeTrue())
		Expect(VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, Identity: "signer@registry.example"}.ValidForAddon()).To(BeTrue())
		Expect(VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern}.ValidForAddon()).To(BeFalse())
	})

	It("says in words why a policy is refused", func() {
		Expect(VerificationPolicy{SourceRepository: pinRepo}.Check()).To(MatchError(ContainSubstring("needs a required signature")))
		Expect(VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern, SourceRepository: "http://x"}.Check()).To(MatchError(ContainSubstring("https URL")))
		Expect(VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: "^a|b$"}.Check()).To(MatchError(ErrInvalid))
		Expect(patternPinned.Check()).To(Succeed())
	})
})

var _ = Describe("source repository pin", func() {
	It("names what was checked in the signer label", func() {
		Expect(VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, Identity: "signer@registry.example"}.SignerLabel()).To(Equal("signer@registry.example"))
		Expect(VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, Identity: "signer@registry.example", SourceRepository: pinRepo}.SignerLabel()).To(Equal("signer@registry.example"))
		Expect(patternPinned.SignerLabel()).To(Equal(pinRepo))
		// A pattern alone names no one signer, so nothing is claimed.
		Expect(VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: pinPattern}.SignerLabel()).To(BeEmpty())
		Expect(VerificationPolicy{}.SignerLabel()).To(BeEmpty())
		Expect(pinnedSigner(Source{ID: "mine", URL: "oci://registry.example/libs/x:latest", Policy: patternPinned})).To(Equal(pinRepo))
	})

	It("passes every signer field to the verifier", func() {
		c := patternPinned.CosignPolicy()
		Expect(c.Issuer).To(Equal(pinIssuer))
		Expect(c.Identity).To(BeEmpty())
		Expect(c.IdentityRegex).To(Equal(pinPattern))
		Expect(c.SourceRepository).To(Equal(pinRepo))
		exact := VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, Identity: "signer@registry.example"}.CosignPolicy()
		Expect(exact.Identity).To(Equal("signer@registry.example"))
		Expect(exact.IdentityRegex).To(BeEmpty())
		Expect(exact.SourceRepository).To(BeEmpty())
	})

	It("survives a save and a reload of the store", func() {
		path := filepath.Join(GinkgoT().TempDir(), "sources")
		s, err := NewSources(path, func(context.Context, contract.Actor) error { return nil }, CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		src := Source{ID: "lib", Name: "lib", URL: "oci://registry.example/libs/fixture-addon:1", Enabled: true, Policy: patternPinned}
		Expect(s.Save(ctx, admin, src)).To(Succeed())
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(pinRepo))
		again, err := NewSources(path, func(context.Context, contract.Actor) error { return nil }, CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		got, err := again.Get(ctx, admin, "lib")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Policy).To(Equal(patternPinned))
		// The wire form uses the names the other policy fields use.
		b, err := json.Marshal(got.Policy)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"sourceRepository":"` + pinRepo + `"`))
	})
})
