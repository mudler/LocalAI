package application

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("extractModelGroupsFromConfigs", func() {
	It("returns an empty map when no config declares groups", func() {
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "a"},
			{Name: "b"},
		})
		Expect(out).To(BeEmpty())
	})

	It("returns each model's normalized groups", func() {
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "a", ConcurrencyGroups: []string{" heavy ", "vision", "heavy"}},
			{Name: "b", ConcurrencyGroups: []string{"heavy"}},
			{Name: "c"}, // no groups → omitted
		})
		Expect(out).To(HaveLen(2))
		Expect(out["a"]).To(Equal([]string{"heavy", "vision"}))
		Expect(out["b"]).To(Equal([]string{"heavy"}))
		Expect(out).ToNot(HaveKey("c"))
	})

	It("omits models whose groups normalize to empty", func() {
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "blanks", ConcurrencyGroups: []string{"", "  "}},
		})
		Expect(out).To(BeEmpty())
	})

	It("skips disabled models so they cannot block loading after re-enable", func() {
		disabled := true
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "a", ConcurrencyGroups: []string{"heavy"}, Disabled: &disabled},
			{Name: "b", ConcurrencyGroups: []string{"heavy"}},
		})
		Expect(out).To(HaveLen(1))
		Expect(out).To(HaveKey("b"))
		Expect(out).ToNot(HaveKey("a"))
	})
})
