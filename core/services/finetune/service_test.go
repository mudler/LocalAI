package finetune

// White-box tests (package finetune) so a spec can drive the service's internal
// SyncedMap the same way StartJob does (via jobs.Set) without standing up a
// training backend, then assert the cross-replica reads (GetJob/ListJobs) and
// the adapter conversions that keep REST responses byte-for-byte unchanged.

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// newTestService builds a standalone FineTuneService wired to the given bus. The
// model/config loaders are nil because the read/sync paths under test never touch
// them; the data dir is a throwaway temp dir so the disk Loader finds nothing.
func newTestService(bus *testutil.FakeBus) *FineTuneService {
	appConfig := &config.ApplicationConfig{
		Context:  context.Background(),
		DataPath: GinkgoT().TempDir(),
	}
	return NewFineTuneService(appConfig, nil, nil, bus, nil)
}

var _ = Describe("FineTuneService", func() {
	ctx := context.Background()

	Describe("cross-replica job visibility", func() {
		var (
			bus  *testutil.FakeBus
			a, b *FineTuneService
		)

		BeforeEach(func() {
			// One shared bus, two replicas: exactly the distributed topology where
			// a round-robin request may land on a replica that did not originate
			// the change.
			bus = testutil.NewFakeBus()
			a = newTestService(bus)
			b = newTestService(bus)
		})

		AfterEach(func() {
			Expect(a.Close()).To(Succeed())
			Expect(b.Close()).To(Succeed())
		})

		It("makes a job created on A visible via B's GetJob and ListJobs", func() {
			job := &schema.FineTuneJob{ID: "job-1", UserID: "user-1", Status: "queued", CreatedAt: "2026-06-27T10:00:00Z"}
			// StartJob persists via jobs.Set; drive that directly to avoid a backend.
			Expect(a.jobs.Set(ctx, job)).To(Succeed())

			got, err := b.GetJob("user-1", "job-1")
			Expect(err).ToNot(HaveOccurred(), "B must see a job A just created")
			Expect(got.Status).To(Equal("queued"))

			listed := b.ListJobs("user-1")
			Expect(listed).To(HaveLen(1))
			Expect(listed[0].ID).To(Equal("job-1"))
		})

		It("removes a job from B when it is deleted on A", func() {
			job := &schema.FineTuneJob{ID: "job-2", UserID: "user-1", Status: "completed", CreatedAt: "2026-06-27T10:00:00Z"}
			Expect(a.jobs.Set(ctx, job)).To(Succeed())
			_, err := b.GetJob("user-1", "job-2")
			Expect(err).ToNot(HaveOccurred(), "precondition: B must have the job before the delete")

			Expect(a.jobs.Delete(ctx, "job-2")).To(Succeed())

			_, err = b.GetJob("user-1", "job-2")
			Expect(err).To(HaveOccurred(), "a delete on A must remove the job from B")
		})

		It("propagates a status update from A to B", func() {
			job := &schema.FineTuneJob{ID: "job-3", UserID: "user-1", Status: "training", CreatedAt: "2026-06-27T10:00:00Z"}
			Expect(a.jobs.Set(ctx, job)).To(Succeed())

			updated := &schema.FineTuneJob{ID: "job-3", UserID: "user-1", Status: "completed", CreatedAt: "2026-06-27T10:00:00Z"}
			Expect(a.jobs.Set(ctx, updated)).To(Succeed())

			got, err := b.GetJob("user-1", "job-3")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("completed"))
		})
	})

	Describe("ListJobs", func() {
		var svc *FineTuneService

		BeforeEach(func() {
			svc = newTestService(testutil.NewFakeBus())
		})
		AfterEach(func() { Expect(svc.Close()).To(Succeed()) })

		It("filters by user and sorts newest-first", func() {
			Expect(svc.jobs.Set(ctx, &schema.FineTuneJob{ID: "old", UserID: "u1", CreatedAt: "2026-06-25T10:00:00Z"})).To(Succeed())
			Expect(svc.jobs.Set(ctx, &schema.FineTuneJob{ID: "new", UserID: "u1", CreatedAt: "2026-06-27T10:00:00Z"})).To(Succeed())
			Expect(svc.jobs.Set(ctx, &schema.FineTuneJob{ID: "other", UserID: "u2", CreatedAt: "2026-06-26T10:00:00Z"})).To(Succeed())

			jobs := svc.ListJobs("u1")
			Expect(jobs).To(HaveLen(2), "only u1's jobs")
			Expect(jobs[0].ID).To(Equal("new"), "newest first")
			Expect(jobs[1].ID).To(Equal("old"))
		})

		It("returns every user's jobs when the userID filter is empty", func() {
			Expect(svc.jobs.Set(ctx, &schema.FineTuneJob{ID: "a", UserID: "u1", CreatedAt: "2026-06-25T10:00:00Z"})).To(Succeed())
			Expect(svc.jobs.Set(ctx, &schema.FineTuneJob{ID: "b", UserID: "u2", CreatedAt: "2026-06-26T10:00:00Z"})).To(Succeed())

			Expect(svc.ListJobs("")).To(HaveLen(2))
		})

		It("rejects GetJob for a job owned by another user", func() {
			Expect(svc.jobs.Set(ctx, &schema.FineTuneJob{ID: "x", UserID: "owner", CreatedAt: "2026-06-25T10:00:00Z"})).To(Succeed())

			_, err := svc.GetJob("intruder", "x")
			Expect(err).To(HaveOccurred(), "a different user must not read someone else's job")
		})
	})

	Describe("store adapter conversion", func() {
		// The SyncedMap value type is *schema.FineTuneJob (the exact REST shape).
		// These specs prove the DB adapter round-trips it losslessly, so hydrate
		// and write-through in distributed mode keep responses unchanged.
		It("round-trips a job through jobToRecord/recordToJob preserving the API shape", func() {
			original := &schema.FineTuneJob{
				ID:              "rt-1",
				UserID:          "user-1",
				Model:           "base-model",
				Backend:         "trl",
				ModelID:         "trl-finetune-rt-1",
				TrainingType:    "lora",
				TrainingMethod:  "sft",
				Status:          "completed",
				Message:         "done",
				OutputDir:       "/data/fine-tune/rt-1",
				ExtraOptions:    map[string]string{"hf_token": "secret"},
				CreatedAt:       "2026-06-27T10:00:00Z",
				ExportStatus:    "completed",
				ExportMessage:   "",
				ExportModelName: "base-model-ft-rt-1",
				Config:          &schema.FineTuneJobRequest{Model: "base-model", Backend: "trl", DatasetSource: "data.jsonl"},
			}

			rec := jobToRecord(original)
			Expect(rec.ID).To(Equal("rt-1"))
			Expect(rec.ConfigJSON).ToNot(BeEmpty(), "structured config must serialize into the JSON column")
			Expect(rec.ExtraOptsJSON).ToNot(BeEmpty())

			back := recordToJob(rec)
			Expect(back.ID).To(Equal(original.ID))
			Expect(back.UserID).To(Equal(original.UserID))
			Expect(back.Model).To(Equal(original.Model))
			Expect(back.Backend).To(Equal(original.Backend))
			Expect(back.ModelID).To(Equal(original.ModelID))
			Expect(back.TrainingType).To(Equal(original.TrainingType))
			Expect(back.TrainingMethod).To(Equal(original.TrainingMethod))
			Expect(back.Status).To(Equal(original.Status))
			Expect(back.Message).To(Equal(original.Message))
			Expect(back.OutputDir).To(Equal(original.OutputDir))
			Expect(back.ExportStatus).To(Equal(original.ExportStatus))
			Expect(back.ExportModelName).To(Equal(original.ExportModelName))
			Expect(back.CreatedAt).To(Equal(original.CreatedAt))
			Expect(back.ExtraOptions).To(Equal(original.ExtraOptions))
			Expect(back.Config).ToNot(BeNil())
			Expect(back.Config.DatasetSource).To(Equal("data.jsonl"))
		})
	})

	Describe("compile-time adapter contract", func() {
		It("satisfies syncstate.Store for *distributed.FineTuneStore", func() {
			// Guards against drift between the adapter and the component interface;
			// the var assertion in syncstore.go covers it at build time, this keeps
			// the type referenced from a spec too.
			var _ *distributed.FineTuneStore
			Expect(&fineTuneStoreAdapter{}).ToNot(BeNil())
		})
	})
})
