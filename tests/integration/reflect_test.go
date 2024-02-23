package integration_test

import (
	"reflect"

	"github.com/go-skynet/LocalAI/core/config"
	model "github.com/go-skynet/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Integration Tests involving reflection in liue of code generation", func() {
	Context("config.TemplateConfig and model.TemplateType must stay in sync", func() {

		ttc := reflect.TypeOf(config.TemplateConfig{})

		It("TemplateConfig and TemplateType should have the same number of valid values", func() {
			const lastValidTemplateType = model.IntegrationTestTemplate - 1
			Expect(lastValidTemplateType).To(Equal(ttc.NumField()))
		})

	})
})
