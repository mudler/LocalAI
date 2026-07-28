package galleryop_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The record the Activity page reads. In standalone mode it is the in-memory
// ring; in distributed mode it must come from PostgreSQL, or each replica
// answers with its own private history and a replica added by a scale-out
// answers with nothing.
var _ = Describe("OpCache history source", func() {
	var (
		svc   *galleryop.GalleryService
		cache *galleryop.OpCache
	)

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		cache = galleryop.NewOpCache(svc)
	})

	// recordLocally drives an operation through the local terminal path, which
	// is the only thing that puts an entry in the in-memory ring.
	recordLocally := func(key, jobID string) {
		cache.SetBackend(key, jobID)
		svc.UpdateStatus(jobID, &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})
		cache.DeleteUUID(jobID)
	}

	Context("standalone, with no gallery store wired", func() {
		It("answers from the in-memory ring", func() {
			recordLocally("llama-cpp", "job-local")

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].JobID).To(Equal("job-local"))
			Expect(history[0].Name).To(Equal("llama-cpp"))
		})

		It("clears the in-memory ring", func() {
			recordLocally("llama-cpp", "job-local")
			Expect(cache.ClearHistory()).To(Succeed())
			Expect(cache.History()).To(BeEmpty())
		})
	})

	Context("distributed, with a gallery store wired", func() {
		var (
			store *distributed.GalleryStore
			db    *gorm.DB
		)

		BeforeEach(func() {
			db = testutil.SetupTestDB()
			var err error
			store, err = distributed.NewGalleryStore(db)
			Expect(err).ToNot(HaveOccurred())
			cache.SetGalleryStore(store)
		})

		// breakStore makes every subsequent store read and write fail, which is
		// what a database outage looks like from the OpCache's side.
		breakStore := func() {
			Expect(db.Migrator().DropTable(&distributed.GalleryOperationRecord{})).To(Succeed())
		}

		// seed writes a finished operation the way a peer replica would have
		// left it: a row created for the job, then driven to a terminal status.
		seed := func(jobID, cacheKey, elementName, opType, status, errMsg string, isBackend bool) {
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID:                 jobID,
				GalleryElementName: elementName,
				OpType:             opType,
				Status:             "pending",
			})).To(Succeed())
			if cacheKey != "" {
				Expect(store.UpsertCacheKey(jobID, cacheKey, isBackend)).To(Succeed())
			}
			Expect(store.UpdateStatus(jobID, status, errMsg)).To(Succeed())
			time.Sleep(5 * time.Millisecond)
		}

		It("answers from the store rather than the local ring", func() {
			// A local operation this replica happened to serve, plus one it
			// never saw. Only the store knows about both.
			recordLocally("llama-cpp", "job-local")
			seed("job-peer", "localai@qwen3-4b", "localai@qwen3-4b", "model_install", "completed", "", false)

			history := cache.History()
			Expect(history).To(HaveLen(1),
				"the store is the whole record; the ring must not be merged in on top of it")
			Expect(history[0].JobID).To(Equal("job-peer"))
			Expect(history[0].ID).To(Equal("localai@qwen3-4b"))
			Expect(history[0].Name).To(Equal("qwen3-4b"), "the gallery prefix is not part of the name")
			Expect(history[0].Outcome).To(Equal(galleryop.OutcomeCompleted))
			Expect(history[0].TaskType).To(Equal("installation"))
			Expect(history[0].IsBackend).To(BeFalse())
			Expect(history[0].NodeID).To(BeEmpty())
			Expect(history[0].StartedAt).ToNot(BeZero())
			Expect(history[0].FinishedAt).ToNot(BeZero())
		})

		It("reports a removal as a deletion", func() {
			seed("job-del", "localai@qwen3-4b", "localai@qwen3-4b", "model_delete", "completed", "", false)

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].TaskType).To(Equal("deletion"))
		})

		It("reports a backend removal as a deletion too", func() {
			seed("job-bdel", "vllm", "vllm", "backend_delete", "completed", "", true)

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].TaskType).To(Equal("deletion"))
			Expect(history[0].IsBackend).To(BeTrue())
		})

		It("splits a node-scoped key into the bare backend name and the node", func() {
			seed("job-node", galleryop.NodeScopedKey("worker-7", "llama-cpp"), "llama-cpp",
				"backend_install", "completed", "", true)

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].Name).To(Equal("llama-cpp"))
			Expect(history[0].NodeID).To(Equal("worker-7"))
			Expect(history[0].IsBackend).To(BeTrue())
		})

		It("falls back to the gallery element name when the cache key never landed", func() {
			// UpsertCacheKey runs on admission, after the row exists; a job that
			// failed in between has a row and no cache key.
			seed("job-nokey", "", "localai@qwen3-4b", "model_install", "failed", "disk full", false)

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].ID).To(Equal("localai@qwen3-4b"))
			Expect(history[0].Name).To(Equal("qwen3-4b"))
			Expect(history[0].Outcome).To(Equal(galleryop.OutcomeFailed))
			Expect(history[0].Error).To(Equal("disk full"))
		})

		It("reads a backend operation off the op type when the cache key never landed", func() {
			// is_backend_op is only ever written by UpsertCacheKey, so the rows
			// that need the name fallback would report a backend operation as a
			// model one and file it under the wrong page filter.
			seed("job-nokey", "", "vllm", "backend_install", "failed", "oci pull failed", false)

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].IsBackend).To(BeTrue())
		})

		It("carries a cancellation through as cancelled", func() {
			seed("job-cancel", "localai@qwen3-4b", "localai@qwen3-4b", "model_install", "cancelled", "", false)

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].Outcome).To(Equal(galleryop.OutcomeCancelled))
		})

		It("returns the record newest first", func() {
			seed("job-1", "localai@one", "localai@one", "model_install", "completed", "", false)
			seed("job-2", "localai@two", "localai@two", "model_install", "completed", "", false)
			seed("job-3", "localai@three", "localai@three", "model_install", "completed", "", false)

			history := cache.History()
			Expect(history).To(HaveLen(3))
			Expect(history[0].JobID).To(Equal("job-3"))
			Expect(history[1].JobID).To(Equal("job-2"))
			Expect(history[2].JobID).To(Equal("job-1"))
		})

		It("omits operations still in flight", func() {
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "job-running", GalleryElementName: "busy", OpType: "model_install", Status: "downloading",
			})).To(Succeed())

			Expect(cache.History()).To(BeEmpty())
		})

		// The cancel handler persists "cancelled" synchronously, then the
		// handler goroutine unwinds with the context error and Start hands that
		// to updateError. If the second write wins, the page renders a
		// cancelled install as a red failure card offering Retry, with a raw
		// "context canceled" as the reason.
		It("records a cancelled install as cancelled, not as a context-cancelled failure", func() {
			svc.SetGalleryStore(store)
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "job-cancel", GalleryElementName: "localai@qwen3-4b",
				OpType: "model_install", Status: "downloading",
			})).To(Succeed())
			Expect(store.UpsertCacheKey("job-cancel", "localai@qwen3-4b", false)).To(Succeed())

			Expect(store.Cancel("job-cancel")).To(Succeed())
			svc.UpdateStatus("job-cancel", &galleryop.OpStatus{
				Error: context.Canceled, Processed: true, Message: "error: context canceled",
			})

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].Outcome).To(Equal(galleryop.OutcomeCancelled))
			Expect(history[0].Error).To(BeEmpty())
		})

		It("falls back to the local ring when the store read fails", func() {
			recordLocally("llama-cpp", "job-local")
			seed("job-peer", "localai@qwen3-4b", "localai@qwen3-4b", "model_install", "completed", "", false)

			// A database blip must not blank the page: the ring holds a subset
			// of the same record, which beats returning nothing.
			breakStore()

			history := cache.History()
			Expect(history).To(HaveLen(1))
			Expect(history[0].JobID).To(Equal("job-local"))
		})

		It("clears the record for the whole cluster, leaving live operations", func() {
			recordLocally("llama-cpp", "job-local")
			seed("job-peer", "localai@qwen3-4b", "localai@qwen3-4b", "model_install", "completed", "", false)
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "job-running", GalleryElementName: "busy", OpType: "model_install", Status: "downloading",
			})).To(Succeed())

			Expect(cache.ClearHistory()).To(Succeed())

			Expect(cache.History()).To(BeEmpty())

			active, err := store.ListActive()
			Expect(err).ToNot(HaveOccurred())
			ids := []string{}
			for _, op := range active {
				ids = append(ids, op.ID)
			}
			Expect(ids).To(ContainElement("job-running"))
		})

		It("empties the local ring too, so a store failure cannot resurrect the record", func() {
			recordLocally("llama-cpp", "job-local")
			Expect(cache.ClearHistory()).To(Succeed())
			breakStore()

			Expect(cache.History()).To(BeEmpty())
		})

		// Reporting success on a clear that did not happen makes the record
		// vanish from the page and come straight back on the next fetch, with
		// nothing said about why.
		It("reports a failed clear instead of claiming success", func() {
			recordLocally("llama-cpp", "job-local")
			breakStore()

			Expect(cache.ClearHistory()).ToNot(Succeed())
			Expect(cache.History()).To(HaveLen(1),
				"a failed clear must leave the fallback record intact rather than fake an empty one")
		})
	})
})
