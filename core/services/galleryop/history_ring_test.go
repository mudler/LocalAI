package galleryop

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// The ring's dedupe and its bounded seen set are not reachable deterministically
// through the exported API: a second DeleteUUID for the same job finds no cache
// key and returns before add is ever called. They are driven directly here so
// the guards are pinned rather than incidentally uncovered.
var _ = Describe("opHistory", func() {
	It("refuses a job ID it already recorded", func() {
		h := newOpHistory(DefaultHistorySize)

		Expect(h.add(OpRecord{JobID: "job-1", Name: "llama-cpp"})).To(BeTrue())
		Expect(h.add(OpRecord{JobID: "job-1", Name: "llama-cpp"})).To(BeFalse())
		Expect(h.list()).To(HaveLen(1))
	})

	It("forgets a job ID once its record is evicted, so seen stays bounded", func() {
		h := newOpHistory(3)

		for i := 1; i <= 4; i++ {
			Expect(h.add(OpRecord{JobID: fmt.Sprintf("job-%d", i)})).To(BeTrue())
		}
		Expect(h.list()).To(HaveLen(3))

		// job-1 fell off the back, so its ID is free again. If seen kept every
		// ID ever added it would grow for the lifetime of the process.
		Expect(h.add(OpRecord{JobID: "job-1"})).To(BeTrue())
		Expect(h.list()).To(HaveLen(3))
	})

	It("clears the seen set alongside the records", func() {
		h := newOpHistory(DefaultHistorySize)

		Expect(h.add(OpRecord{JobID: "job-1"})).To(BeTrue())
		h.clear()

		Expect(h.list()).To(BeEmpty())
		Expect(h.add(OpRecord{JobID: "job-1"})).To(BeTrue())
	})
})

// Recording paths that only the in-package tests can reach, because they start
// from an OpCache entry that Set/SetBackend never created.
var _ = Describe("OpCache terminal recording (internal)", func() {
	var (
		svc   *GalleryService
		cache *OpCache
	)

	BeforeEach(func() {
		svc = NewGalleryService(&config.ApplicationConfig{}, nil)
		cache = NewOpCache(svc)
	})

	It("falls back to the finish time when the start was never stamped", func() {
		// hydrateFromStore and applyStart write status directly, so an op
		// recovered from PostgreSQL or replicated from a peer has no stamp. A
		// zero StartedAt would render as a two-millennia duration on the
		// Activity page, so the record reports a zero-length op instead.
		cache.applyStart(OpCacheEvent{CacheKey: "vllm", JobID: "job-hydrated", IsBackend: true})
		svc.UpdateStatus("job-hydrated", &OpStatus{Processed: true, Progress: 100, Message: "completed"})

		cache.DeleteUUID("job-hydrated")

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].StartedAt).NotTo(BeZero())
		Expect(history[0].StartedAt).To(Equal(history[0].FinishedAt))
	})

	It("drops the previous job's start stamp when a key is reused", func() {
		// Reinstalling the same backend reuses the cache key with a fresh job
		// ID. Without the drop, every retry would orphan a timestamp.
		cache.Set("qwen3-8b", "job-old")
		cache.Set("qwen3-8b", "job-new")

		Expect(cache.started.Exists("job-old")).To(BeFalse())
		Expect(cache.started.Exists("job-new")).To(BeTrue())
	})

	It("drops the start stamp once the operation is recorded", func() {
		cache.SetBackend("piper", "job-done")
		svc.UpdateStatus("job-done", &OpStatus{Processed: true, Progress: 100, Message: "completed"})

		cache.DeleteUUID("job-done")

		Expect(cache.started.Exists("job-done")).To(BeFalse())
	})

	It("drops the previous job's start stamp when a peer reuses the cache key", func() {
		// A retry admitted on another replica reaches us as a start event for
		// the same key with a fresh job ID. The stamp the key used to point at
		// is now unreachable, so it has to go with it.
		cache.Set("qwen3-8b", "job-local")

		cache.applyStart(OpCacheEvent{CacheKey: "qwen3-8b", JobID: "job-peer"})

		Expect(cache.started.Exists("job-local")).To(BeFalse())
	})

	It("drops the start stamp when the end event finds no cache key", func() {
		// The end broadcast can land before the local Set has written its status
		// key, and recordTerminal returns before its own stamp cleanup when no
		// key maps to the job. The operation is over cluster-wide either way, so
		// the stamp must not outlive it.
		cache.started.Set("job-raced", time.Now())

		cache.ApplyEndForTest("job-raced")

		Expect(cache.started.Exists("job-raced")).To(BeFalse())
	})

	It("records nothing for an end event whose gallery status is gone", func() {
		// A replica that restarted mid-operation hydrates its cache keys from
		// PostgreSQL, but gallery statuses live in memory only and come back
		// empty. A missing status on this path means the outcome was lost, not
		// that the op never started, so reading it as cancelled would file a
		// successful install as a failure.
		cache.applyStart(OpCacheEvent{CacheKey: "vllm", JobID: "job-restarted", IsBackend: true})

		cache.ApplyEndForTest("job-restarted")

		Expect(cache.History()).To(BeEmpty())
	})

	It("still records a locally dismissed op with no status as cancelled", func() {
		// The local reading of a missing status is unchanged: nothing ever set
		// one, so the op was queued, never started, then removed.
		cache.Set("qwen3-4b", "job-queued")

		cache.DeleteUUID("job-queued")

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].Outcome).To(Equal(OutcomeCancelled))
	})

	It("records one entry when both terminal paths reach the ring", func() {
		// The external spec that fires DeleteUUID then the broadcast does not
		// reach the ring twice: the first call removes the status keys, so the
		// second returns before add. recordTerminal leaves the keys alone, so
		// calling it twice is what actually exercises the dedupe.
		cache.SetBackend("whisper-cpp", "job-twice")
		svc.UpdateStatus("job-twice", &OpStatus{Processed: true, Progress: 100, Message: "completed"})

		cache.recordTerminal("job-twice", terminalLocal)
		cache.recordTerminal("job-twice", terminalPeer)

		Expect(cache.History()).To(HaveLen(1))
	})

	It("keeps the finish-time fallback when the start stamp reads as zero", func() {
		// A concurrent recordTerminal for the same job deletes the stamp, and a
		// missing key reads back as the zero time. Letting that zero win over the
		// fallback serialises StartedAt as year one, which the Activity page
		// renders as a two-millennia run.
		cache.SetBackend("bark-cpp", "job-zero")
		cache.started.Set("job-zero", time.Time{})
		svc.UpdateStatus("job-zero", &OpStatus{Processed: true, Progress: 100, Message: "completed"})

		cache.DeleteUUID("job-zero")

		history := cache.History()
		Expect(history).To(HaveLen(1))
		Expect(history[0].StartedAt).NotTo(BeZero())
		Expect(history[0].StartedAt).To(Equal(history[0].FinishedAt))
	})
})
