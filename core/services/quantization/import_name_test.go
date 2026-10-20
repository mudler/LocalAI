package quantization

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ImportModel name collision", func() {
	var (
		svc    *QuantizationService
		models string
	)

	BeforeEach(func() {
		models = GinkgoT().TempDir()
		appConfig := &config.ApplicationConfig{
			Context:     context.Background(),
			DataPath:    GinkgoT().TempDir(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: models}},
		}
		svc = NewQuantizationService(appConfig, nil, nil, testutil.NewFakeBus(), nil)
		Expect(svc.jobs.Set(context.Background(), &schema.QuantizationJob{ID: "job-1", Model: "base", Status: "completed", OutputFile: "/out/model.gguf"})).To(Succeed())
	})

	refused := func(name string) {
		_, err := svc.ImportModel(context.Background(), "", "job-1", schema.QuantizationImportRequest{Name: name})
		Expect(err).To(MatchError(ContainSubstring("already exists")))
	}

	It("refuses a name that a config file already holds", func() {
		Expect(os.WriteFile(filepath.Join(models, "taken.yaml"), []byte("name: taken"), 0600)).To(Succeed())
		refused("taken")
	})

	It("refuses a name that a folder already holds, with no config file", func() {
		Expect(os.Mkdir(filepath.Join(models, "taken"), 0750)).To(Succeed())
		refused("taken")
	})

	It("refuses a name that a file of another extension already holds", func() {
		Expect(os.WriteFile(filepath.Join(models, "taken.gguf"), []byte("x"), 0600)).To(Succeed())
		refused("taken")
	})
})
