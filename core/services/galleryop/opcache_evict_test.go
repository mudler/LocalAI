package galleryop_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// These specs reproduce the distributed "Reinstall spins forever" bug:
// processingBackends (the UI spinner source) is built from OpCache.GetStatus,
// which historically returned every cached op unconditionally. Cleanup only
// happened when a client polled /api/backends/job/:uid, but the Manage-page
// Reinstall/Upgrade buttons never poll, so a completed install stayed in
// processingBackends forever. GetStatus must self-evict terminal ops.
var _ = Describe("OpCache.GetStatus eviction", func() {
	var (
		svc   *galleryop.GalleryService
		cache *galleryop.OpCache
	)

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		cache = galleryop.NewOpCache(svc)
	})

	It("keeps an op that is still processing", func() {
		cache.SetBackend("llama-cpp", "uuid-1")
		svc.UpdateStatus("uuid-1", &galleryop.OpStatus{Message: "processing backend: llama-cpp", Progress: 0})
		processing, _ := cache.GetStatus()
		Expect(processing).To(HaveKeyWithValue("llama-cpp", "uuid-1"))
		Expect(cache.Exists("llama-cpp")).To(BeTrue())
	})

	It("evicts a completed op so it no longer shows as processing", func() {
		cache.SetBackend("llama-cpp", "uuid-1")
		svc.UpdateStatus("uuid-1", &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})
		processing, _ := cache.GetStatus()
		Expect(processing).NotTo(HaveKey("llama-cpp"))
		Expect(cache.Exists("llama-cpp")).To(BeFalse())
	})

	It("evicts a failed op", func() {
		cache.SetBackend("piper", "uuid-2")
		svc.UpdateStatus("uuid-2", &galleryop.OpStatus{Processed: true, Error: errors.New("boom")})
		processing, _ := cache.GetStatus()
		Expect(processing).NotTo(HaveKey("piper"))
		Expect(cache.Exists("piper")).To(BeFalse())
	})

	It("evicts a cancelled op", func() {
		cache.SetBackend("vllm", "uuid-3")
		svc.UpdateStatus("uuid-3", &galleryop.OpStatus{Processed: true, Cancelled: true, Message: "cancelled"})
		processing, _ := cache.GetStatus()
		Expect(processing).NotTo(HaveKey("vllm"))
	})

	It("does not evict an op with no status yet (queued)", func() {
		cache.SetBackend("whisper", "uuid-4")
		processing, taskTypes := cache.GetStatus()
		Expect(processing).To(HaveKeyWithValue("whisper", "uuid-4"))
		Expect(taskTypes).To(HaveKeyWithValue("whisper", "Waiting"))
	})
})
