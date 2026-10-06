// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// These specs run against a real database. They check what a caller can see
// (can the model load again, did the stale owner stop) and not which SQL ran.
var _ = Describe("Load job generation fencing", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		ctx      context.Context
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx = context.Background()
	})

	backdate := func(trackingKey string, age time.Duration) {
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", trackingKey).
			Update("last_progress", time.Now().Add(-age)).Error).To(Succeed())
	}

	loadJobCount := func(trackingKey string) int64 {
		var n int64
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", trackingKey).Count(&n).Error).To(Succeed())
		return n
	}

	Describe("stale owner writes", func() {
		It("returns no rows for a heartbeat, fail and delete of a replaced attempt", func() {
			a, claimed, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(a.Generation).ToNot(BeEmpty())
			Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())

			b, claimed, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(b.Generation).ToNot(Equal(a.Generation))

			for _, stale := range []LoadJobRef{a.Ref(), {TrackingKey: "aba-model"}} {
				Expect(registry.UpdateLoadJob(ctx, stale, LoadJobUpdate{State: LoadJobStateLoading})).To(MatchError(ErrStaleLoadJob))
				Expect(registry.FailLoadJob(ctx, stale, "late failure")).To(MatchError(ErrStaleLoadJob))
				Expect(registry.DeleteLoadJob(ctx, stale)).To(MatchError(ErrStaleLoadJob))
			}

			current, err := registry.GetLoadJob(ctx, "aba-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(current.Ref()).To(Equal(b.Ref()))
			Expect(current.State).To(Equal(LoadJobStatePending), "a stale write must not touch the current attempt")
		})

		It("does not report a database failure as a lost generation", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "cancelled-write", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			writeErr := registry.UpdateLoadJob(cancelled, job.Ref(), LoadJobUpdate{})
			Expect(writeErr).To(HaveOccurred())
			Expect(writeErr).ToNot(MatchError(ErrStaleLoadJob))
		})

		It("refuses a replica publish from a replaced attempt and from a context with no ownership", func() {
			node := &BackendNode{Name: "worker-1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
			Expect(registry.Register(ctx, node, true)).To(Succeed())

			a, _, err := registry.ClaimLoadJob(ctx, "publish-model", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
			b, _, err := registry.ClaimLoadJob(ctx, "publish-model", "frontend-b")
			Expect(err).ToNot(HaveOccurred())

			staleCtx := withLoadOwnership(ctx, a.Ref())
			Expect(registry.SetNodeModel(staleCtx, node.ID, "publish-model", 0, "staging", "10.0.0.1:9001", 0)).To(MatchError(ErrStaleLoadJob))
			Expect(registry.SetNodeModelLoadInfo(staleCtx, node.ID, "publish-model", 0, "llama-cpp", []byte("x"))).To(MatchError(ErrStaleLoadJob))
			Expect(registry.UpsertModelLoadInfo(staleCtx, "publish-model", "llama-cpp", []byte("x"))).To(MatchError(ErrStaleLoadJob))
			Expect(registry.RemoveNodeModel(staleCtx, node.ID, "publish-model", 0)).To(MatchError(ErrStaleLoadJob))

			// The load path never runs without an ownership value. If it does,
			// the write is refused instead of waved through.
			unowned := withLoadPath(ctx)
			Expect(registry.SetNodeModel(unowned, node.ID, "publish-model", 0, "staging", "10.0.0.1:9001", 0)).To(MatchError(ErrLoadOwnershipMissing))

			var rows int64
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", "publish-model").Count(&rows).Error).To(Succeed())
			Expect(rows).To(BeZero(), "no fenced write may leave a replica row behind")

			ownerCtx := withLoadOwnership(withLoadPath(ctx), b.Ref())
			Expect(registry.SetNodeModel(ownerCtx, node.ID, "publish-model", 0, "staging", "10.0.0.1:9001", 0)).To(Succeed())
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", "publish-model").Count(&rows).Error).To(Succeed())
			Expect(rows).To(Equal(int64(1)))

			// Callers outside the load path are unaffected.
			Expect(registry.SetNodeModel(ctx, node.ID, "other-model", 0, "loaded", "10.0.0.1:9002", 0)).To(Succeed())
		})
	})

	Describe("failed jobs", func() {
		It("frees a failed model without manual SQL, even after a frontend restart", func() {
			failed, _, err := registry.ClaimLoadJob(ctx, "retry-model", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, failed.Ref(), "worker out of disk")).To(Succeed())

			// Inside the grace window every caller sees the real cause.
			got, claimed, err := registry.ClaimLoadJob(ctx, "retry-model", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(got.State).To(Equal(LoadJobStateFailed))
			Expect(got.LastError).To(Equal("worker out of disk"))

			backdate("retry-model", loadJobFailureGrace+time.Second)

			// A new registry stands in for a restarted frontend: the release
			// must not depend on an in-process timer.
			restarted, err := NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())
			fresh, claimed, err := restarted.ClaimLoadJob(ctx, "retry-model", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue(), "a failed job past its grace window must not block the model")
			Expect(fresh.Generation).ToNot(Equal(failed.Generation))
			Expect(loadJobCount("retry-model")).To(Equal(int64(1)))

			// The old owner cannot touch the new attempt.
			Expect(registry.UpdateLoadJob(ctx, failed.Ref(), LoadJobUpdate{State: LoadJobStateLoading})).To(MatchError(ErrStaleLoadJob))
			Expect(registry.DeleteLoadJob(ctx, failed.Ref())).To(MatchError(ErrStaleLoadJob))
		})

		It("does not let the success path delete a failed job", func() {
			failed, _, err := registry.ClaimLoadJob(ctx, "failed-delete", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, failed.Ref(), "boom")).To(Succeed())
			Expect(registry.DeleteLoadJob(ctx, failed.Ref())).To(MatchError(ErrStaleLoadJob))
			Expect(loadJobCount("failed-delete")).To(Equal(int64(1)))

			// The failed row is removed by its own, grace-gated call.
			Expect(registry.DeleteFailedLoadJob(ctx, failed.Ref())).To(MatchError(ErrStaleLoadJob))
			backdate("failed-delete", loadJobFailureGrace+time.Second)
			Expect(registry.DeleteFailedLoadJob(ctx, failed.Ref())).To(Succeed())
			Expect(loadJobCount("failed-delete")).To(BeZero())
		})
	})

	Describe("legacy rows", func() {
		// An old binary created the table without a generation column. A row
		// there has a NULL generation once the column exists.
		legacySchema := func() *gorm.DB {
			legacyDB := testutil.SetupTestDB()
			Expect(legacyDB.Exec(`CREATE TABLE model_load_jobs (
				tracking_key varchar(255) PRIMARY KEY, state varchar(16) NOT NULL, owner_replica varchar(64),
				node_id varchar(36), node_name varchar(255), replica_index bigint, bytes_sent bigint,
				total_bytes bigint, file_index bigint, total_files bigint, last_error text,
				started_at timestamptz, created_at timestamptz, updated_at timestamptz, last_progress timestamptz)`).Error).To(Succeed())
			return legacyDB
		}
		insertLegacy := func(legacyDB *gorm.DB, key string, age time.Duration) {
			Expect(legacyDB.Exec(`INSERT INTO model_load_jobs (tracking_key, state, owner_replica, last_progress, created_at, updated_at)
				VALUES (?, 'staging', 'old-frontend', ?, now(), now())`, key, time.Now().Add(-age)).Error).To(Succeed())
		}

		It("gives a pre-existing row a generation and reclaims it once its owner stops heartbeating", func() {
			legacyDB := legacySchema()
			insertLegacy(legacyDB, "legacy-dead", 5*time.Minute)
			insertLegacy(legacyDB, "legacy-live", time.Second)

			migrated, err := NewNodeRegistry(legacyDB)
			Expect(err).ToNot(HaveOccurred())

			var jobs []ModelLoadJob
			Expect(legacyDB.Find(&jobs).Error).To(Succeed())
			Expect(jobs).To(HaveLen(2))
			for _, j := range jobs {
				Expect(j.Generation).ToNot(BeEmpty(), "the migration must give every legacy row a generation")
			}

			// A live legacy owner keeps its job: we wait on it.
			live, claimed, err := migrated.ClaimLoadJob(ctx, "legacy-live", "new-frontend")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(live.OwnerReplica).To(Equal("old-frontend"))

			// A dead one does not block the model.
			dead, claimed, err := migrated.ClaimLoadJob(ctx, "legacy-dead", "new-frontend")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(dead.OwnerReplica).To(Equal("new-frontend"))
			Expect(dead.Generation).ToNot(BeEmpty())
		})

		It("treats a row written later by an old binary (empty generation) the same way", func() {
			legacyDB := legacySchema()
			migrated, err := NewNodeRegistry(legacyDB)
			Expect(err).ToNot(HaveOccurred())
			insertLegacy(legacyDB, "late-legacy", 5*time.Minute)

			var row ModelLoadJob
			Expect(legacyDB.First(&row, "tracking_key = ?", "late-legacy").Error).To(Succeed())
			Expect(row.Generation).To(BeEmpty())

			// No new owner can hold an empty generation, so no write may match it.
			Expect(migrated.UpdateLoadJob(ctx, row.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
			Expect(migrated.FailLoadJob(ctx, row.Ref(), "x")).To(MatchError(ErrStaleLoadJob))
			Expect(migrated.DeleteLoadJob(ctx, row.Ref())).To(MatchError(ErrStaleLoadJob))

			job, claimed, err := migrated.ClaimLoadJob(ctx, "late-legacy", "new-frontend")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(job.Generation).ToNot(BeEmpty())
		})
	})

	Describe("the owner loop", func() {
		var (
			router   *SmartRouter
			unloader *fakeUnloader
			backend  *stubBackend
			node     *BackendNode
		)

		BeforeEach(func() {
			node = &BackendNode{
				Name: "worker-1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
				TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000,
			}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			backend = &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
			unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"}}
			router = NewSmartRouter(registry, SmartRouterOptions{
				Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}, DB: db,
			})
		})

		replaceJob := func(key string) LoadJobRef {
			Expect(db.Where("tracking_key = ?", key).Delete(&ModelLoadJob{}).Error).To(Succeed())
			next, claimed, err := registry.ClaimLoadJob(ctx, key, "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			return next.Ref()
		}

		It("cancels the work when another generation took the model, and leaves that generation alone", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "stale-owner", "frontend-a")
			Expect(err).ToNot(HaveOccurred())

			workCtx := make(chan context.Context, 1)
			result := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				result <- router.runLoadOwner(ctx, job.Ref(), func(c context.Context) error {
					workCtx <- c
					<-c.Done()
					return c.Err()
				})
			}()
			var running context.Context
			Eventually(workCtx).Should(Receive(&running))

			next := replaceJob("stale-owner")

			Eventually(running.Done(), 10*time.Second).Should(BeClosed(), "the stale owner must stop its own work")
			var ownerErr error
			Eventually(result, 10*time.Second).Should(Receive(&ownerErr))
			Expect(errors.Is(ownerErr, ErrStaleLoadJob)).To(BeTrue(), "the stale error must reach the caller, not be swallowed")

			current, err := registry.GetLoadJob(ctx, "stale-owner")
			Expect(err).ToNot(HaveOccurred())
			Expect(current).ToNot(BeNil())
			Expect(current.Ref()).To(Equal(next))
			Expect(current.State).To(Equal(LoadJobStatePending), "the stale owner must not fail or delete the new attempt")
		})

		It("deletes the job on success and records the cause on failure", func() {
			ok, _, err := registry.ClaimLoadJob(ctx, "owner-ok", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(router.runLoadOwner(ctx, ok.Ref(), func(context.Context) error { return nil })).To(Succeed())
			Expect(loadJobCount("owner-ok")).To(BeZero())

			bad, _, err := registry.ClaimLoadJob(ctx, "owner-bad", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			err = router.runLoadOwner(ctx, bad.Ref(), func(context.Context) error { return errors.New("remote load failed") })
			Expect(err).To(MatchError(ContainSubstring("remote load failed")))
			row, err := registry.GetLoadJob(ctx, "owner-bad")
			Expect(err).ToNot(HaveOccurred())
			Expect(row.State).To(Equal(LoadJobStateFailed))
			Expect(row.LastError).To(ContainSubstring("remote load failed"))
		})

		It("wakes only the waiters of the generation that finished", func() {
			a, _, err := registry.ClaimLoadJob(ctx, "waiters", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
			b, _, err := registry.ClaimLoadJob(ctx, "waiters", "frontend-b")
			Expect(err).ToNot(HaveOccurred())

			waiter := router.loadWaiterChan(loadWaiterKey(b.Ref()))
			Expect(router.finishLoadJob(ctx, a.Ref())).To(MatchError(ErrStaleLoadJob))
			Consistently(waiter, 200*time.Millisecond).ShouldNot(BeClosed(), "a delayed finish of an old generation must not wake the new one")
			Expect(router.finishLoadJob(ctx, b.Ref())).To(Succeed())
			Eventually(waiter).Should(BeClosed())
		})

		It("releases a waiter registration when the waiter leaves early", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "leaving", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			waiter := router.loadWaiterChan(loadWaiterKey(job.Ref()))
			Expect(router.waitForLoadJob(waitCtx, job.Ref(), waiter)).To(HaveOccurred())
			router.loadWaitersMu.Lock()
			defer router.loadWaitersMu.Unlock()
			Expect(router.loadWaiters).ToNot(HaveKey(loadWaiterKey(job.Ref())))
		})

		It("retries a model whose first load failed, with no SQL in between", func() {
			// The remote load fails with an error the backend answered.
			backend.loadResult = &pb.Result{Success: false, Message: "unsupported architecture"}
			opts := &pb.ModelOptions{Model: "models/retry.gguf"}
			_, err := router.Route(ctx, "retry-route", "models/retry.gguf", "llama-cpp", "", opts, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported architecture"))

			var failed ModelLoadJob
			Eventually(func() string {
				_ = db.First(&failed, "tracking_key = ?", "retry-route").Error
				return failed.State
			}, 5*time.Second, 50*time.Millisecond).Should(Equal(LoadJobStateFailed))

			// Inside the grace window the caller gets the real cause quickly.
			_, err = router.Route(ctx, "retry-route", "models/retry.gguf", "llama-cpp", "", opts, false)
			Expect(err).To(MatchError(ContainSubstring("unsupported architecture")))

			// The backend is fixed. The grace window ends. No manual cleanup.
			backend.mu.Lock()
			backend.loadResult = &pb.Result{Success: true}
			backend.mu.Unlock()
			backdate("retry-route", loadJobFailureGrace+time.Second)

			res, err := router.Route(ctx, "retry-route", "models/retry.gguf", "llama-cpp", "", opts, false)
			Expect(err).ToNot(HaveOccurred())
			res.Release()
			Eventually(func() int64 { return loadJobCount("retry-route") }, 5*time.Second, 50*time.Millisecond).Should(BeZero())
		})

		It("runs the reconciler path under the same owner loop", func() {
			const modelName = "reconciled"
			blob, err := proto.Marshal(&pb.ModelOptions{Model: "models/reconciled.gguf"})
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.UpsertModelLoadInfo(ctx, modelName, "llama-cpp", blob)).To(Succeed())

			release := make(chan struct{})
			started := make(chan struct{})
			unloader.installHook = func() { close(started); <-release }
			defer close(release)

			result := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, err := router.ScheduleAndLoadModel(ctx, modelName, nil)
				result <- err
			}()
			Eventually(started, 10*time.Second).Should(BeClosed())

			// The reconciler load is a durable job, so a request for the same
			// model waits for it instead of scheduling a second copy.
			held, err := registry.GetLoadJob(ctx, modelName)
			Expect(err).ToNot(HaveOccurred())
			Expect(held).ToNot(BeNil())
			_, claimed, err := registry.ClaimLoadJob(ctx, modelName, "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())

			next := replaceJob(modelName)

			var loadErr error
			Eventually(result, 10*time.Second).Should(Receive(&loadErr))
			Expect(errors.Is(loadErr, ErrStaleLoadJob)).To(BeTrue(), "the reconciler path must stop when it loses the generation")

			current, err := registry.GetLoadJob(ctx, modelName)
			Expect(err).ToNot(HaveOccurred())
			Expect(current.Ref()).To(Equal(next))
			Expect(current.State).To(Equal(LoadJobStatePending))
		})

		It("does not start a reconciler load while another owner holds the model", func() {
			const modelName = "held-model"
			blob, err := proto.Marshal(&pb.ModelOptions{Model: "models/held.gguf"})
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.UpsertModelLoadInfo(ctx, modelName, "llama-cpp", blob)).To(Succeed())
			_, claimed, err := registry.ClaimLoadJob(ctx, modelName, "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())

			_, err = router.ScheduleAndLoadModel(ctx, modelName, nil)
			Expect(err).To(HaveOccurred())
			unloader.mu.Lock()
			defer unloader.mu.Unlock()
			Expect(unloader.installCalls).To(BeEmpty())
		})
	})
})

// Every write of a durable load job must carry the generation predicate, so
// the predicate lives in one helper. This guards against a new write that
// bypasses it.
var _ = Describe("Load job write discipline", func() {
	It("keeps every ModelLoadJob write inside model_load_job.go", func() {
		write := regexp.MustCompile(`\.(Model|Delete|Update|Updates|UpdateColumn|Save|Exec)\((&ModelLoadJob\{\}|"[^"]*model_load_jobs)|Table\("model_load_jobs"\)`)
		files, err := filepath.Glob("*.go")
		Expect(err).ToNot(HaveOccurred())
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") || f == "model_load_job.go" {
				continue
			}
			src, err := os.ReadFile(f)
			Expect(err).ToNot(HaveOccurred())
			Expect(write.Match(src)).To(BeFalse(), "%s writes model_load_jobs outside the ownedLoadJob helper", f)
		}
	})

	It("builds every write in model_load_job.go from ownedLoadJob", func() {
		src, err := os.ReadFile("model_load_job.go")
		Expect(err).ToNot(HaveOccurred())
		// The helper is the only place a query on the table starts.
		Expect(strings.Count(string(src), "Model(&ModelLoadJob{})")).To(Equal(1))
		// A delete with a free-form condition would skip the predicate.
		Expect(string(src)).ToNot(MatchRegexp(`Delete\(&ModelLoadJob\{\},`))
	})
})
