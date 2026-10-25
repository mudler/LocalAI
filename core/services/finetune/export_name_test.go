package finetune

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

var _ = Describe("ExportModel name collision", func() {
	var (
		svc    *FineTuneService
		models string
	)

	BeforeEach(func() {
		models = GinkgoT().TempDir()
		appConfig := &config.ApplicationConfig{
			Context:     context.Background(),
			DataPath:    GinkgoT().TempDir(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: models}},
		}
		svc = NewFineTuneService(appConfig, nil, nil, testutil.NewFakeBus(), nil)
		Expect(svc.jobs.Set(context.Background(), &schema.FineTuneJob{ID: "job-1", Model: "base", Status: "completed"})).To(Succeed())
	})

	refused := func(name string) {
		_, err := svc.ExportModel(context.Background(), "", "job-1", schema.ExportRequest{Name: name, ExportFormat: "lora"})
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
