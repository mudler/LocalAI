package galleryop_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// The history ring is the record behind the Activity page. It is bounded so a
// long-running instance cannot grow it without limit, deduped by job ID so the
// local eviction and the NATS end event for the same job record once, and
// newest-first so the page renders without sorting.
var _ = Describe("OpCache history ring", func() {
	var (
		svc   *galleryop.GalleryService
		cache *galleryop.OpCache
	)

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		cache = galleryop.NewOpCache(svc)
	})

	It("starts empty", func() {
		Expect(cache.History()).To(BeEmpty())
	})

	It("returns records newest first", func() {
		for i := 1; i <= 3; i++ {
			key := fmt.Sprintf("model-%d", i)
			job := fmt.Sprintf("job-%d", i)
			cache.Set(key, job)
			svc.UpdateStatus(job, &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})
			cache.DeleteUUID(job)
		}
		history := cache.History()
		Expect(history).To(HaveLen(3))
		Expect(history[0].Name).To(Equal("model-3"))
		Expect(history[2].Name).To(Equal("model-1"))
	})

	It("caps at DefaultHistorySize and evicts the oldest first", func() {
		total := galleryop.DefaultHistorySize + 5
		for i := 1; i <= total; i++ {
			key := fmt.Sprintf("model-%d", i)
			job := fmt.Sprintf("job-%d", i)
			cache.Set(key, job)
			svc.UpdateStatus(job, &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})
			cache.DeleteUUID(job)
		}
		history := cache.History()
		Expect(history).To(HaveLen(galleryop.DefaultHistorySize))
		Expect(history[0].Name).To(Equal(fmt.Sprintf("model-%d", total)))
		// model-1..model-5 fell off the back.
		Expect(history[len(history)-1].Name).To(Equal("model-6"))
	})

	It("clears on request", func() {
		cache.Set("model-a", "job-a")
		svc.UpdateStatus("job-a", &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})
		cache.DeleteUUID("job-a")
		Expect(cache.History()).To(HaveLen(1))

		Expect(cache.ClearHistory()).To(Succeed())
		Expect(cache.History()).To(BeEmpty())
	})
})
