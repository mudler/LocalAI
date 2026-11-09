package quantization

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ImportModel claim", func() {
	var (
		svc      *QuantizationService
		models   string
		launched atomic.Int32
	)

	importStatus := func() string {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		job, ok := svc.jobs.Get("job-1")
		Expect(ok).To(BeTrue())
		return job.ImportStatus
	}

	BeforeEach(func() {
		launched.Store(0)
		models = GinkgoT().TempDir()
		appConfig := &config.ApplicationConfig{
			Context:     context.Background(),
			DataPath:    GinkgoT().TempDir(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: models}},
		}
		svc = NewQuantizationService(appConfig, nil, nil, testutil.NewFakeBus(), nil)
		// Do not run the import body: it needs a real backend. The claim is
		// what is under test, and it stays held because the body never ends.
		svc.launchImport = func(func()) { launched.Add(1) }
		Expect(svc.jobs.Set(context.Background(), &schema.QuantizationJob{ID: "job-1", Model: "base", Status: "completed", OutputFile: "/out/model.gguf"})).To(Succeed())
	})

	It("lets exactly one of many concurrent imports for a job proceed", func() {
		const callers = 16
		var (
			wg    sync.WaitGroup
			start = make(chan struct{})
			errs  = make([]error, callers)
		)
		for i := range callers {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				_, errs[i] = svc.ImportModel(context.Background(), "", "job-1",
					schema.QuantizationImportRequest{Name: fmt.Sprintf("out-%d", i)})
			}()
		}
		close(start)
		wg.Wait()

		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++
				continue
			}
			Expect(err).To(MatchError(ContainSubstring("import already in progress")))
		}
		Expect(succeeded).To(Equal(1))
		Expect(launched.Load()).To(BeEquivalentTo(1))
		Expect(importStatus()).To(Equal("importing"))
	})

	It("keeps the previous status when the name collides", func() {
		Expect(os.WriteFile(filepath.Join(models, "taken.yaml"), []byte("name: taken"), 0600)).To(Succeed())
		_, err := svc.ImportModel(context.Background(), "", "job-1", schema.QuantizationImportRequest{Name: "taken"})
		Expect(err).To(MatchError(ContainSubstring("already exists")))
		Expect(importStatus()).To(BeEmpty())
		Expect(launched.Load()).To(BeZero())
	})

	It("keeps the previous status when the name is invalid", func() {
		_, err := svc.ImportModel(context.Background(), "", "job-1", schema.QuantizationImportRequest{Name: "../escape"})
		Expect(err).To(MatchError(ContainSubstring("invalid model name")))
		Expect(importStatus()).To(BeEmpty())
		Expect(launched.Load()).To(BeZero())
	})

	It("restores a failed status after a validation error, then allows a retry", func() {
		svc.mu.Lock()
		job, _ := svc.jobs.Get("job-1")
		job.ImportStatus = "failed"
		svc.mu.Unlock()

		Expect(os.Mkdir(filepath.Join(models, "taken"), 0750)).To(Succeed())
		_, err := svc.ImportModel(context.Background(), "", "job-1", schema.QuantizationImportRequest{Name: "taken"})
		Expect(err).To(HaveOccurred())
		Expect(importStatus()).To(Equal("failed"))

		_, err = svc.ImportModel(context.Background(), "", "job-1", schema.QuantizationImportRequest{Name: "fresh"})
		Expect(err).ToNot(HaveOccurred())
		Expect(importStatus()).To(Equal("importing"))
	})
})
