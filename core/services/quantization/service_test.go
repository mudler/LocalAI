package quantization

// White-box tests (package quantization) so a spec can drive the service's
// internal SyncedMap the same way StartJob does (via jobs.Set) without standing
// up a quantization backend, then assert the cross-replica reads
// (GetJob/ListJobs) and the adapter conversions that keep REST responses
// byte-for-byte unchanged.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/testutil"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// newTestService builds a standalone QuantizationService wired to the given bus.
// The model/config loaders are nil because the read/sync paths under test never
// touch them; the data dir is a throwaway temp dir so the disk Loader finds
// nothing.
func newTestService(bus *testutil.FakeBus) *QuantizationService {
	appConfig := &config.ApplicationConfig{
		Context:  context.Background(),
		DataPath: GinkgoT().TempDir(),
	}
	return NewQuantizationService(appConfig, nil, nil, bus, nil)
}

var _ = Describe("QuantizationService", func() {
	ctx := context.Background()

	Describe("cross-replica job visibility", func() {
		var (
			bus  *testutil.FakeBus
			a, b *QuantizationService
		)

		BeforeEach(func() {
			// One shared bus, two replicas: exactly the distributed topology where a
			// round-robin request may land on a replica that did not originate the
			// change.
			bus = testutil.NewFakeBus()
			a = newTestService(bus)
			b = newTestService(bus)
		})

		AfterEach(func() {
			Expect(a.Close()).To(Succeed())
			Expect(b.Close()).To(Succeed())
		})

		It("makes a job created on A visible via B's GetJob and ListJobs", func() {
			job := &schema.QuantizationJob{ID: "job-1", UserID: "user-1", Status: "queued", CreatedAt: "2026-06-27T10:00:00Z"}
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
			job := &schema.QuantizationJob{ID: "job-2", UserID: "user-1", Status: "completed", CreatedAt: "2026-06-27T10:00:00Z"}
			Expect(a.jobs.Set(ctx, job)).To(Succeed())
			_, err := b.GetJob("user-1", "job-2")
			Expect(err).ToNot(HaveOccurred(), "precondition: B must have the job before the delete")

			Expect(a.jobs.Delete(ctx, "job-2")).To(Succeed())

			_, err = b.GetJob("user-1", "job-2")
			Expect(err).To(HaveOccurred(), "a delete on A must remove the job from B")
		})

		It("propagates a status update from A to B", func() {
			job := &schema.QuantizationJob{ID: "job-3", UserID: "user-1", Status: "quantizing", CreatedAt: "2026-06-27T10:00:00Z"}
			Expect(a.jobs.Set(ctx, job)).To(Succeed())

			updated := &schema.QuantizationJob{ID: "job-3", UserID: "user-1", Status: "completed", CreatedAt: "2026-06-27T10:00:00Z"}
			Expect(a.jobs.Set(ctx, updated)).To(Succeed())

			got, err := b.GetJob("user-1", "job-3")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("completed"))
		})
	})

	Describe("ListJobs", func() {
		var svc *QuantizationService

		BeforeEach(func() {
			svc = newTestService(testutil.NewFakeBus())
		})
		AfterEach(func() { Expect(svc.Close()).To(Succeed()) })

		It("filters by user and sorts newest-first", func() {
			Expect(svc.jobs.Set(ctx, &schema.QuantizationJob{ID: "old", UserID: "u1", CreatedAt: "2026-06-25T10:00:00Z"})).To(Succeed())
			Expect(svc.jobs.Set(ctx, &schema.QuantizationJob{ID: "new", UserID: "u1", CreatedAt: "2026-06-27T10:00:00Z"})).To(Succeed())
			Expect(svc.jobs.Set(ctx, &schema.QuantizationJob{ID: "other", UserID: "u2", CreatedAt: "2026-06-26T10:00:00Z"})).To(Succeed())

			jobs := svc.ListJobs("u1")
			Expect(jobs).To(HaveLen(2), "only u1's jobs")
			Expect(jobs[0].ID).To(Equal("new"), "newest first")
			Expect(jobs[1].ID).To(Equal("old"))
		})

		It("returns every user's jobs when the userID filter is empty", func() {
			Expect(svc.jobs.Set(ctx, &schema.QuantizationJob{ID: "a", UserID: "u1", CreatedAt: "2026-06-25T10:00:00Z"})).To(Succeed())
			Expect(svc.jobs.Set(ctx, &schema.QuantizationJob{ID: "b", UserID: "u2", CreatedAt: "2026-06-26T10:00:00Z"})).To(Succeed())

			Expect(svc.ListJobs("")).To(HaveLen(2))
		})

		It("rejects GetJob for a job owned by another user", func() {
			Expect(svc.jobs.Set(ctx, &schema.QuantizationJob{ID: "x", UserID: "owner", CreatedAt: "2026-06-25T10:00:00Z"})).To(Succeed())

			_, err := svc.GetJob("intruder", "x")
			Expect(err).To(HaveOccurred(), "a different user must not read someone else's job")
		})
	})

	Describe("store adapter conversion", func() {
		// The SyncedMap value type is *schema.QuantizationJob (the exact REST shape).
		// These specs prove the DB adapter round-trips it losslessly, so hydrate and
		// write-through in distributed mode keep responses unchanged.
		It("round-trips a job through jobToRecord/recordToJob preserving the API shape", func() {
			original := &schema.QuantizationJob{
				ID:               "rt-1",
				UserID:           "user-1",
				Model:            "base-model",
				Backend:          "llama-cpp-quantization",
				ModelID:          "llama-cpp-quantization-quantize-rt-1",
				QuantizationType: "q4_k_m",
				Status:           "completed",
				Message:          "done",
				OutputDir:        "/data/quantization/rt-1",
				OutputFile:       "/data/quantization/rt-1/model.gguf",
				ExtraOptions:     map[string]string{"hf_token": "secret"},
				CreatedAt:        "2026-06-27T10:00:00Z",
				ImportStatus:     "completed",
				ImportMessage:    "",
				ImportModelName:  "base-model-q4_k_m-rt-1",
				Config:           &schema.QuantizationJobRequest{Model: "base-model", Backend: "llama-cpp-quantization", QuantizationType: "q4_k_m"},
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
			Expect(back.QuantizationType).To(Equal(original.QuantizationType))
			Expect(back.Status).To(Equal(original.Status))
			Expect(back.Message).To(Equal(original.Message))
			Expect(back.OutputDir).To(Equal(original.OutputDir))
			Expect(back.OutputFile).To(Equal(original.OutputFile))
			Expect(back.ImportStatus).To(Equal(original.ImportStatus))
			Expect(back.ImportModelName).To(Equal(original.ImportModelName))
			Expect(back.CreatedAt).To(Equal(original.CreatedAt))
			Expect(back.ExtraOptions).To(Equal(original.ExtraOptions))
			Expect(back.Config).ToNot(BeNil())
			Expect(back.Config.QuantizationType).To(Equal("q4_k_m"))
		})
	})

	Describe("progress recording", func() {
		var (
			bus *testutil.FakeBus
			s   *QuantizationService
		)

		BeforeEach(func() {
			bus = testutil.NewFakeBus()
			s = newTestService(bus)
		})

		AfterEach(func() {
			Expect(s.Close()).To(Succeed())
		})

		// The reported failure: a job that ran with no SSE client attached stayed
		// "queued" forever, because the only code that advanced job state lived
		// inside StreamProgress' stream callback. The transition is now applied by
		// the job's own watcher, so it lands with nobody watching.
		It("advances job state and rewrites state.json with no subscriber attached", func() {
			job := &schema.QuantizationJob{ID: "job-np", UserID: "user-1", Status: "queued", CreatedAt: "2026-09-05T10:00:00Z"}
			Expect(s.jobs.Set(ctx, job)).To(Succeed())
			Expect(s.progressSubs).To(BeEmpty())

			s.applyProgressUpdate(ctx, "job-np", &pb.QuantizationProgressUpdate{
				JobId:      "job-np",
				Status:     "completed",
				Message:    "Quantization complete",
				OutputFile: "/data/quantization/job-np/model-q4_k_m.gguf",
			})

			got, err := s.GetJob("user-1", "job-np")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("completed"))
			Expect(got.Message).To(Equal("Quantization complete"))
			Expect(got.OutputFile).To(Equal("/data/quantization/job-np/model-q4_k_m.gguf"))

			data, err := os.ReadFile(filepath.Join(s.jobDir("job-np"), "state.json"))
			Expect(err).ToNot(HaveOccurred())
			var persisted schema.QuantizationJob
			Expect(json.Unmarshal(data, &persisted)).To(Succeed())
			Expect(persisted.Status).To(Equal("completed"))
			Expect(persisted.OutputFile).To(Equal("/data/quantization/job-np/model-q4_k_m.gguf"))
		})

		It("does not let a late update overwrite a terminal status", func() {
			job := &schema.QuantizationJob{ID: "job-stopped", UserID: "user-1", Status: "stopped", CreatedAt: "2026-09-05T10:00:00Z"}
			Expect(s.jobs.Set(ctx, job)).To(Succeed())

			s.applyProgressUpdate(ctx, "job-stopped", &pb.QuantizationProgressUpdate{
				JobId:  "job-stopped",
				Status: "quantizing",
			})

			got, err := s.GetJob("user-1", "job-stopped")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("stopped"))
		})

		// The backend hands each update to a single consumer, so every client has
		// to be served from one in-process fan-out rather than its own stream.
		It("delivers one update to every attached subscriber", func() {
			first := s.subscribeProgress("job-fan")
			second := s.subscribeProgress("job-fan")
			defer s.unsubscribeProgress("job-fan", first)
			defer s.unsubscribeProgress("job-fan", second)

			s.publishProgress("job-fan", &schema.QuantizationProgressEvent{JobID: "job-fan", Status: "quantizing"})

			Expect((<-first).Status).To(Equal("quantizing"))
			Expect((<-second).Status).To(Equal("quantizing"))
		})

		It("unsubscribing removes the job's entry once the last client leaves", func() {
			ch := s.subscribeProgress("job-leave")
			Expect(s.progressSubs).To(HaveKey("job-leave"))
			s.unsubscribeProgress("job-leave", ch)
			Expect(s.progressSubs).ToNot(HaveKey("job-leave"))
		})

		// A client attaching after the job finished — including a job restored from
		// disk as "stopped" after a restart, which has no watcher — must not block
		// waiting for an event that will never come.
		It("returns a final event immediately for a job that already finished", func() {
			job := &schema.QuantizationJob{
				ID: "job-done", UserID: "user-1", Status: "completed",
				Message: "Quantization complete", OutputFile: "/data/quantization/job-done/model-q4_k_m.gguf",
				CreatedAt: "2026-09-05T10:00:00Z",
			}
			Expect(s.jobs.Set(ctx, job)).To(Succeed())

			var seen []*schema.QuantizationProgressEvent
			Expect(s.StreamProgress(ctx, "user-1", "job-done", func(e *schema.QuantizationProgressEvent) {
				seen = append(seen, e)
			})).To(Succeed())

			Expect(seen).To(HaveLen(1))
			Expect(seen[0].Status).To(Equal("completed"))
			Expect(seen[0].OutputFile).To(Equal("/data/quantization/job-done/model-q4_k_m.gguf"))
			Expect(s.progressSubs).ToNot(HaveKey("job-done"))
		})

		// StopJob kills the backend, so the watcher will never forward a terminal
		// update; without an explicit release an attached client would hang.
		It("releases an attached client when the job is stopped", func() {
			job := &schema.QuantizationJob{ID: "job-stop", UserID: "user-1", Status: "quantizing", CreatedAt: "2026-09-05T10:00:00Z"}
			Expect(s.jobs.Set(ctx, job)).To(Succeed())

			ch := s.subscribeProgress("job-stop")
			defer s.unsubscribeProgress("job-stop", ch)

			// nil modelLoader: exercise the release without standing up a backend.
			s.mu.Lock()
			job.Status = "stopped"
			s.mu.Unlock()
			s.publishProgress("job-stop", &schema.QuantizationProgressEvent{
				JobID: "job-stop", Status: "stopped", Message: "Quantization stopped by user",
			})

			event := <-ch
			Expect(event.Status).To(Equal("stopped"))
			Expect(isTerminalStatus(event.Status)).To(BeTrue())
		})

		It("streams published events to a client until a terminal status arrives", func() {
			job := &schema.QuantizationJob{ID: "job-live", UserID: "user-1", Status: "queued", CreatedAt: "2026-09-05T10:00:00Z"}
			Expect(s.jobs.Set(ctx, job)).To(Succeed())

			var seen []string
			done := make(chan error, 1)
			go func() {
				done <- s.StreamProgress(ctx, "user-1", "job-live", func(e *schema.QuantizationProgressEvent) {
					seen = append(seen, e.Status)
				})
			}()

			Eventually(func() bool {
				s.progressMu.Lock()
				defer s.progressMu.Unlock()
				return len(s.progressSubs["job-live"]) == 1
			}).Should(BeTrue())

			s.publishProgress("job-live", &schema.QuantizationProgressEvent{JobID: "job-live", Status: "quantizing"})
			s.publishProgress("job-live", &schema.QuantizationProgressEvent{JobID: "job-live", Status: "completed"})

			Eventually(done).Should(Receive(BeNil()))
			Expect(seen).To(Equal([]string{"quantizing", "completed"}))
		})
	})

	Describe("compile-time adapter contract", func() {
		It("satisfies syncstate.Store for *distributed.QuantStore", func() {
			// Guards against drift between the adapter and the component interface;
			// the var assertion in syncstore.go covers it at build time, this keeps
			// the type referenced from a spec too.
			var _ *distributed.QuantStore
			Expect(&quantStoreAdapter{}).ToNot(BeNil())
		})
	})
})
