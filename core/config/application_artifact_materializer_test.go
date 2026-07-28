package config

import (
	"context"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type applicationArtifactMaterializer struct{}

func (*applicationArtifactMaterializer) Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error) {
	return modelartifacts.Result{}, nil
}

var _ = Describe("ApplicationConfig model artifact materializer", func() {
	It("provides a default materializer", func() {
		Expect(NewApplicationConfig().ModelArtifactMaterializer).NotTo(BeNil())
	})

	It("accepts an injected materializer without exposing it to serialization", func() {
		materializer := &applicationArtifactMaterializer{}
		appConfig := NewApplicationConfig(WithModelArtifactMaterializer(materializer))
		Expect(appConfig.ModelArtifactMaterializer).To(BeIdenticalTo(materializer))

		field, found := reflect.TypeOf(ApplicationConfig{}).FieldByName("ModelArtifactMaterializer")
		Expect(found).To(BeTrue())
		Expect(field.Tag.Get("json")).To(Equal("-"))
		Expect(field.Tag.Get("yaml")).To(Equal("-"))
	})
})
