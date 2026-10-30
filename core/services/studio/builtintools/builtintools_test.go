package builtintools_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/builtintools"
	"github.com/mudler/LocalAI/pkg/web/webtools"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBuiltinTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "builtintools suite")
}

func settingNames() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(config.RuntimeSettings{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		out[name] = true
	}
	return out
}

var _ = Describe("the registry", func() {
	It("names only runtime settings that exist", func() {
		names := settingNames()
		for _, t := range builtintools.All() {
			Expect(names).To(HaveKey(t.Setting), t.Name)
			for _, o := range t.Options {
				Expect(names).To(HaveKey(o), t.Name)
			}
		}
	})

	It("lists the web tools under their model names", func() {
		for _, name := range []string{webtools.SearchName, webtools.FetchName} {
			t, ok := builtintools.Lookup(name)
			Expect(ok).To(BeTrue(), name)
			Expect(t.Group).To(Equal(builtintools.GroupWeb))
			Expect(t.ReadOnly).To(BeTrue())
		}
		Expect(builtintools.Groups()).To(Equal([]string{"web"}))
		Expect(builtintools.IsGroup("web")).To(BeTrue())
		Expect(builtintools.IsGroup("bash")).To(BeFalse())
	})

	It("hands out copies", func() {
		a := builtintools.All()
		a[0].Options[0] = "changed"
		Expect(builtintools.All()[0].Options[0]).NotTo(Equal("changed"))
	})
})
