package cli

import (
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("library add with an identity pattern", func() {
	const (
		pattern = `^https://github\.com/example-org/shared/\.github/workflows/sign\.yml@refs/tags/v.+$`
		repo    = "https://github.com/example-org/example-repo"
	)
	var f LibraryFlags
	BeforeEach(func() {
		f = LibraryFlags{DataPath: GinkgoT().TempDir(), AddonsOfficialLibrary: true}
	})
	stored := func(id string) (catalog.Source, bool) {
		store, err := f.open()
		Expect(err).NotTo(HaveOccurred())
		for _, e := range store.Entries() {
			if e.ID == id {
				return e, true
			}
		}
		return catalog.Source{}, false
	}

	It("stores the pattern and the source repository the flags give", func() {
		c := LibraryAdd{URL: "oci://registry.example/fixture-addon/library:1", Kind: "addon", ID: "fixture", Format: "library", Issuer: "https://issuer.example", IdentityRegex: pattern, SourceRepository: repo, LibraryFlags: f}
		Expect(c.Run(nil)).To(Succeed())
		e, ok := stored("fixture")
		Expect(ok).To(BeTrue())
		Expect(e.Policy).To(Equal(catalog.VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.example", IdentityRegex: pattern, SourceRepository: repo}))
	})

	It("refuses a pattern without a source repository and names the missing flag", func() {
		c := LibraryAdd{URL: "oci://registry.example/b:1", Kind: "addon", ID: "b", Format: "library", Issuer: "https://issuer.example", IdentityRegex: pattern, LibraryFlags: f}
		Expect(c.Run(nil)).To(MatchError(ContainSubstring("source_repository")))
		_, ok := stored("b")
		Expect(ok).To(BeFalse())
	})
})
