package connectors_test

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

var _ = Describe("the resource list of a binding", func() {
	valid := func(res ...connectors.ResourceEntry) connectors.Binding {
		return connectors.Binding{Name: "n", Kind: "k", AgentID: "a", Resources: res}
	}

	It("derives a stable ID in the shape of a resource ID, whatever the case", func() {
		id := connectors.ResourceID("Acme/Widgets")
		Expect(id).To(MatchRegexp(`^r-[0-9a-f]{10}$`))
		Expect(connectors.ResourceID(" acme/widgets ")).To(Equal(id))
		Expect(connectors.ResourceID("acme/tools")).NotTo(Equal(id))
	})

	It("drops blanks and repeats when it makes entries", func() {
		got := connectors.NewResources([]string{" a/b ", "", "A/B", "c/d"})
		Expect(connectors.ResourceKeys(got)).To(Equal([]string{"a/b", "c/d"}))
	})

	It("accepts a good list and refuses a bad one", func() {
		Expect(connectors.Validate(valid(connectors.NewResources([]string{"a/b"})...))).To(Succeed())
		Expect(connectors.Validate(valid(connectors.ResourceEntry{Key: "a b", ID: connectors.ResourceID("a b")}))).NotTo(Succeed())
		Expect(connectors.Validate(valid(connectors.ResourceEntry{Key: "a/b", ID: "r-0000000000"}))).NotTo(Succeed())
		long := strings.Repeat("x", connectors.MaxResourceKeyRunes+1)
		Expect(connectors.Validate(valid(connectors.NewResources([]string{long})...))).NotTo(Succeed())
		var many []string
		for i := 0; i <= connectors.MaxResources; i++ {
			many = append(many, strings.Repeat("k", 3)+string(rune('a'+i%26))+string(rune('a'+i/26)))
		}
		Expect(connectors.Validate(valid(connectors.NewResources(many)...))).NotTo(Succeed())
		dup := []connectors.ResourceEntry{{Key: "a/b", ID: connectors.ResourceID("a/b")}, {Key: "A/B", ID: connectors.ResourceID("A/B")}}
		Expect(connectors.Validate(valid(dup...))).NotTo(Succeed())
	})

	Describe("Entry", func() {
		sealed := []byte("sealed")
		one := connectors.Binding{ID: "b1", Name: "one", Token: sealed, Resources: connectors.NewResources([]string{"acme/widgets"})}
		two := connectors.Binding{ID: "b2", Name: "two", Token: sealed, Resources: connectors.NewResources([]string{"acme/tools", "Acme/Gadgets"})}

		It("matches a resource of the one binding without regard to case", func() {
			e, err := connectors.Entry(one, "ACME/Widgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(e.Key).To(Equal("acme/widgets"))
			e, err = connectors.Entry(two, "acme/gadgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(e.ID).To(Equal(connectors.ResourceID("acme/gadgets")))
		})

		It("never looks at another binding: a key only the other lists is refused with this list", func() {
			_, err := connectors.Entry(one, "acme/tools")
			var na *connectors.NotAllowedError
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(Equal([]string{"acme/widgets"}))
			_, err = connectors.Entry(two, "acme/widgets")
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(Equal([]string{"Acme/Gadgets", "acme/tools"}))
		})

		It("refuses every resource of a binding with an empty list", func() {
			empty := connectors.Binding{ID: "b4", Token: sealed}
			_, err := connectors.Entry(empty, "acme/widgets")
			var na *connectors.NotAllowedError
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(BeEmpty())
		})

		It("gives no entry to a call that names no resource", func() {
			e, err := connectors.Entry(one, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(e).To(Equal(connectors.ResourceEntry{}))
		})

		It("refuses a binding with no token", func() {
			_, err := connectors.Entry(connectors.Binding{ID: "b3", Resources: one.Resources}, "acme/widgets")
			Expect(err).To(MatchError(connectors.ErrNoCredential))
		})
	})
})
