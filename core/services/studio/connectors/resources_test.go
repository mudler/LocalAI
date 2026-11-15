package connectors_test

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// entryOf is the entry a token binding gives for key, without wildcards.
func entryOf(b connectors.Binding, key string) (connectors.ResourceEntry, error) {
	return connectors.EntryUsable(b, b.HasTokenCredential(), key, false)
}

// wildEntry is entryOf for a kind that declares wildcard resources.
func wildEntry(b connectors.Binding, key string) (connectors.ResourceEntry, error) {
	return connectors.EntryUsable(b, b.HasTokenCredential(), key, true)
}

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
			e, err := entryOf(one, "ACME/Widgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(e.Key).To(Equal("acme/widgets"))
			e, err = entryOf(two, "acme/gadgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(e.ID).To(Equal(connectors.ResourceID("acme/gadgets")))
		})

		It("never looks at another binding: a key only the other lists is refused with this list", func() {
			_, err := entryOf(one, "acme/tools")
			var na *connectors.NotAllowedError
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(Equal([]string{"acme/widgets"}))
			_, err = entryOf(two, "acme/widgets")
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(Equal([]string{"Acme/Gadgets", "acme/tools"}))
		})

		It("refuses every resource of a binding with an empty list", func() {
			empty := connectors.Binding{ID: "b4", Token: sealed}
			_, err := entryOf(empty, "acme/widgets")
			var na *connectors.NotAllowedError
			Expect(errors.As(err, &na)).To(BeTrue())
			Expect(na.Allowed).To(BeEmpty())
		})

		It("gives no entry to a call that names no resource", func() {
			e, err := entryOf(one, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(e).To(Equal(connectors.ResourceEntry{}))
		})

		It("refuses a binding with no token", func() {
			_, err := entryOf(connectors.Binding{ID: "b3", Resources: one.Resources}, "acme/widgets")
			Expect(err).To(MatchError(connectors.ErrNoCredential))
		})
	})
})

var _ = Describe("wildcard resources", func() {
	sealed := []byte("sealed")
	with := func(keys ...string) connectors.Binding {
		return connectors.Binding{ID: "b", Token: sealed, Resources: connectors.NewResources(keys)}
	}

	DescribeTable("MatchResource",
		func(entry, key string, want bool) { Expect(connectors.MatchResource(entry, key)).To(Equal(want)) },
		Entry("exact", "acme/widgets", "acme/widgets", true),
		Entry("exact, case", "Acme/Widgets", "acme/widgets", true),
		Entry("exact is not a prefix", "acme/widgets", "acme/widgets-extra", false),
		Entry("owner wildcard", "acme/*", "acme/widgets", true),
		Entry("owner wildcard, case", "ACME/*", "acme/Widgets", true),
		Entry("other owner", "acme/*", "other-owner/x", false),
		Entry("owner as a prefix", "acme/*", "acme-evil/x", false),
		Entry("owner as a suffix", "acme/*", "evil-acme/x", false),
		Entry("owner alone", "acme/*", "acme", false),
		Entry("empty name", "acme/*", "acme/", false),
		Entry("deeper path", "acme/*", "acme/a/b", false),
		Entry("any", "*", "acme/widgets", true),
		Entry("any matches no empty key", "*", "", false),
		Entry("a key with a star is no name", "*", "acme/*", false),
		Entry("a key with a star under an owner", "acme/*", "acme/wid*", false),
		Entry("a partial star is no wildcard", "acme/wid*", "acme/widgets", false),
		Entry("a star in front is no wildcard", "*/widgets", "acme/widgets", false),
	)

	It("keeps the exact match when the kind has no wildcards", func() {
		b := with("acme/*", "*")
		_, err := entryOf(b, "acme/widgets")
		Expect(err).To(HaveOccurred())
		e, err := entryOf(b, "ACME/*")
		Expect(err).NotTo(HaveOccurred())
		Expect(e.Key).To(Equal("acme/*"))
	})

	It("gives an entry for the real key when a wildcard covers it", func() {
		e, err := wildEntry(with("acme/*"), "Acme/Widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(e.Key).To(Equal("Acme/Widgets"))
		Expect(e.ID).To(Equal(connectors.ResourceID("acme/widgets")))
		_, err = wildEntry(with("acme/*"), "acme-evil/x")
		var na *connectors.NotAllowedError
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(Equal([]string{"acme/*"}))
	})

	It("asks for the wildcard * only with the entry *", func() {
		e, err := wildEntry(with("*"), "*")
		Expect(err).NotTo(HaveOccurred())
		Expect(e.Key).To(Equal("*"))
		_, err = wildEntry(with("acme/*"), "*")
		Expect(err).To(HaveOccurred())
		_, err = wildEntry(with("acme/widgets"), "*")
		Expect(err).To(HaveOccurred())
		_, err = wildEntry(with("acme/*"), "acme/*")
		Expect(err).To(HaveOccurred())
	})

	It("still needs a token", func() {
		b := with("*")
		b.Token = nil
		_, err := wildEntry(b, "a/b")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
	})

	It("validates the wildcards of a kind that declares them", func() {
		ok := func(keys ...string) error { return connectors.ValidateWildcards(connectors.NewResources(keys), true) }
		Expect(ok("*")).To(Succeed())
		Expect(ok("acme/*", "other/*", "x/y")).To(Succeed())
		Expect(ok("*", "acme/x")).NotTo(Succeed())
		Expect(ok("acme/wid*")).NotTo(Succeed())
		Expect(ok("*/x")).NotTo(Succeed())
		Expect(ok("a/*/b")).NotTo(Succeed())
		Expect(ok("**")).NotTo(Succeed())
		Expect(connectors.ValidateWildcards(connectors.NewResources([]string{"a*b"}), false)).To(Succeed())
		Expect(errors.Is(ok("a*b"), connectors.ErrInvalid)).To(BeTrue())
	})
})
