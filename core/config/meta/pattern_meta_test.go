package meta_test

import (
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/meta"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMeta(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "config/meta suite")
}

var _ = Describe("pattern detector field metadata", func() {
	byPath := func() map[string]meta.FieldMeta {
		md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())
		out := make(map[string]meta.FieldMeta, len(md.Fields))
		for _, f := range md.Fields {
			out[f.Path] = f
		}
		return out
	}

	It("renders builtins as a select with the catalogue as options", func() {
		f, ok := byPath()["pii_detection.builtins"]
		Expect(ok).To(BeTrue(), "pii_detection.builtins field should exist")
		Expect(f.Component).To(Equal("pii-builtins-select"))
		Expect(f.Options).NotTo(BeEmpty())
	})

	It("renders custom patterns with the pattern-list editor", func() {
		f, ok := byPath()["pii_detection.patterns"]
		Expect(ok).To(BeTrue(), "pii_detection.patterns field should exist")
		Expect(f.Component).To(Equal("pii-pattern-list"))
	})
})
