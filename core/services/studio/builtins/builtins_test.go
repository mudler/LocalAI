package builtins_test

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

func TestBuiltins(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Built-in tools Suite")
}

var _ = Describe("the registry of built-in tools", func() {
	build := func(context.Context, builtins.Chat) (session.Tool, bool, string) { return session.Tool{Description: "x"}, true, "" }
	It("takes only replaceable tools, once, with a Build", func() {
		r := builtins.NewRegistry()
		Expect(r.Register(builtins.Tool{ID: "bash", Build: build})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "bash", Build: build})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{ID: "write_file", Build: build})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch"})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_search", Build: build})).To(Succeed())
		var ids []string
		for _, t := range r.Tools() {
			ids = append(ids, t.ID)
		}
		Expect(ids).To(Equal([]string{"bash", "web_search"}))
	})

	It("builds the tools that are on, named by ID with the built-in provider", func() {
		r := builtins.NewRegistry()
		on := false
		Expect(r.Register(builtins.Tool{ID: "bash", Build: build, Enabled: func() bool { return on }})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch", Build: func(context.Context, builtins.Chat) (session.Tool, bool, string) {
			return session.Tool{}, false, "no model"
		}})).To(Succeed())
		got := r.Build(context.Background(), builtins.Chat{})
		Expect(got).To(HaveLen(2))
		Expect(got[0]).To(MatchFields(IgnoreExtras, Fields{"ID": Equal("bash"), "OK": BeFalse(), "Reason": Equal("off")}))
		Expect(got[1].Reason).To(Equal("no model"))
		on = true
		got = r.Build(context.Background(), builtins.Chat{})
		Expect(got[0].OK).To(BeTrue())
		Expect(got[0].Tool.Name).To(Equal("bash"))
		Expect(got[0].Tool.Provider).To(Equal(builtins.BuiltinProvider))
	})
})
