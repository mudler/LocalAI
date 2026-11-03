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

	Describe("Pick", func() {
		sealed := []byte("sealed")
		one := connectors.Binding{ID: "b1", Name: "one", Token: sealed, Resources: connectors.NewResources([]string{"acme/widgets"})}
		two := connectors.Binding{ID: "b2", Name: "two", Token: sealed, Resources: connectors.NewResources([]string{"acme/tools", "Acme/Widgets"})}

		It("refuses a person with no usable binding", func() {
			_, _, err := connectors.Pick(nil, "acme/widgets")
			Expect(err).To(MatchError(connectors.ErrNoCredential))
			_, _, err = connectors.Pick([]connectors.Binding{{ID: "b3", Resources: one.Resources}}, "acme/widgets")
			Expect(err).To(MatchError(connectors.ErrNoCredential))
		})

		It("matches a resource without regard to case and takes the first binding that lists it", func() {
			b, e, err := connectors.Pick([]connectors.Binding{one, two}, "ACME/Widgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(b.ID).To(Equal("b1"))
			Expect(e.Key).To(Equal("acme/widgets"))
			b, e, err = connectors.Pick([]connectors.Binding{one, two}, "acme/tools")
			Expect(err).NotTo(HaveOccurred())
			Expect(b.ID).To(Equal("b2"))
			Expect(e.ID).To(Equal(connectors.ResourceID("acme/tools")))
		})

		It("refuses a resource on no list and names what is allowed", func() {
			_, _, err := connectors.Pick([]connectors.Binding{one, two}, "other/repo")
			var na *connectors.NotAllowedError
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(Equal([]string{"Acme/Widgets", "acme/tools", "acme/widgets"}))
		})

		It("refuses every resource of a binding with an empty list", func() {
			empty := connectors.Binding{ID: "b4", Token: sealed}
			_, _, err := connectors.Pick([]connectors.Binding{empty}, "acme/widgets")
			var na *connectors.NotAllowedError
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(BeEmpty())
		})

		It("takes the first usable binding when the call names no resource", func() {
			b, e, err := connectors.Pick([]connectors.Binding{{ID: "b0"}, one}, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(b.ID).To(Equal("b1"))
			Expect(e).To(Equal(connectors.ResourceEntry{}))
		})
	})
})
