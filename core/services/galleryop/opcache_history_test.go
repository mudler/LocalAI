package galleryop_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// What lands in history, for each way an operation can leave the cache.
var _ = Describe("OpCache terminal recording", func() {
	var (
		svc   *galleryop.GalleryService
		cache *galleryop.OpCache
	)

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		cache = galleryop.NewOpCache(svc)
	})

	It("records a completed install evicted by GetStatus", func() {
		cache.SetBackend("llama-cpp", "job-1")
		svc.UpdateStatus("job-1", &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})

		// GetStatus is what the UI polls; it self-evicts terminal ops.
		cache.GetStatus()

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].Outcome).To(Equal("completed"))
		Expect(history[0].Name).To(Equal("llama-cpp"))
		Expect(history[0].JobID).To(Equal("job-1"))
		Expect(history[0].IsBackend).To(BeTrue())
		Expect(history[0].TaskType).To(Equal("installation"))
		Expect(history[0].Error).To(BeEmpty())
		Expect(history[0].StartedAt).NotTo(BeZero())
		Expect(history[0].FinishedAt).NotTo(BeZero())
	})

	It("records a failed install only once it is dismissed", func() {
		cache.SetBackend("sherpa-onnx", "job-2")
		svc.UpdateStatus("job-2", &galleryop.OpStatus{Processed: true, Error: errors.New("no space left on device")})

		// A failure is deliberately never auto-evicted: it stays live so the
		// UI can surface it under "Needs attention".
		cache.GetStatus()
		Expect(cache.History()).To(BeEmpty())
		Expect(cache.Exists("sherpa-onnx")).To(BeTrue())

		// Dismissing is what moves it into the record.
		cache.DeleteUUID("job-2")

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].Outcome).To(Equal("failed"))
		Expect(history[0].Error).To(Equal("no space left on device"))
	})

	It("records an op removed before it was processed as cancelled, not completed", func() {
		// An operation that left the cache while it was still unprocessed never
		// got to finish, so it is not a success. This is what POST
		// /api/operations/:jobID/dismiss produces when it fires on an op that is
		// still in flight. Without the !Processed clause it would be recorded as
		// a successful install.
		cache.Set("deepseek-r1-70b", "job-3")
		svc.UpdateStatus("job-3", &galleryop.OpStatus{Progress: 8, Cancellable: true})

		cache.DeleteUUID("job-3")

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].Outcome).To(Equal("cancelled"))
	})

	It("records an errored op as failed even when it never reached Processed", func() {
		// Pins the order of the outcome arms: an error outweighs the
		// "left while unprocessed" reading, so this is a failure to surface,
		// not a cancellation to ignore.
		cache.Set("stable-diffusion-3.5", "job-8")
		svc.UpdateStatus("job-8", &galleryop.OpStatus{Error: errors.New("download interrupted")})

		cache.DeleteUUID("job-8")

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].Outcome).To(Equal("failed"))
		Expect(history[0].Error).To(Equal("download interrupted"))
	})

	It("records an explicitly cancelled op as cancelled", func() {
		cache.Set("flux.1-dev", "job-4")
		svc.UpdateStatus("job-4", &galleryop.OpStatus{Processed: true, Cancelled: true, Message: "cancelled"})

		cache.GetStatus()

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].Outcome).To(Equal("cancelled"))
	})

	It("records a deletion with taskType deletion", func() {
		cache.Set("llama-3.2-1b-instruct", "job-5")
		svc.UpdateStatus("job-5", &galleryop.OpStatus{Processed: true, Progress: 100, Deletion: true, Message: "completed"})

		cache.GetStatus()

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].TaskType).To(Equal("deletion"))
		Expect(history[0].Outcome).To(Equal("completed"))
	})

	It("strips the gallery prefix from the display name", func() {
		cache.Set("localai@qwen3-8b-instruct", "job-6")
		svc.UpdateStatus("job-6", &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})

		cache.GetStatus()

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].ID).To(Equal("localai@qwen3-8b-instruct"))
		Expect(history[0].Name).To(Equal("qwen3-8b-instruct"))
	})

	It("parses a node-scoped key into a backend name plus a node ID", func() {
		cache.SetBackend("node:worker-03:vllm", "job-7")
		svc.UpdateStatus("job-7", &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})

		cache.GetStatus()

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].Name).To(Equal("vllm"))
		Expect(history[0].NodeID).To(Equal("worker-03"))
		Expect(history[0].IsBackend).To(BeTrue())
	})

	It("ignores a job ID that was never in the cache", func() {
		cache.DeleteUUID("job-never-existed")
		Expect(cache.History()).To(BeEmpty())
	})
})
