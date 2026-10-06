package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Load recovery operations projection", func() {
	It("projects cross-frontend cancellation durably after the observer restarts", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		owner, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := owner.ClaimLoadJob(ctx, "cross-frontend", "owner")
		Expect(err).NotTo(HaveOccurred())
		observer, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		result, err := (&nodes.LoadRecoveryService{Registry: observer}).Cancel(ctx, job.Ref(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Outcome).To(Equal(nodes.LoadUncertain))
		restarted, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		router := nodes.NewSmartRouter(restarted, nodes.SmartRouterOptions{})
		router.StagingTracker().ApplyRemote(nodes.StagingProgressEvent{ModelID: job.TrackingKey, Generation: job.Generation, Status: &nodes.StagingStatus{ModelID: job.TrackingKey, Generation: job.Generation, Progress: 99, UpdatedAt: time.Now()}})
		ops := recoveryOperations(recoveryOperationsServer(restarted, router))
		Expect(ops).To(HaveLen(1))
		Expect(ops[0]["job_id"]).To(Equal(job.Generation))
		Expect(ops[0]["terminal"]).To(BeTrue())
		Expect(ops[0]["phase"]).To(Equal("recovery"))
		Expect(ops[0]["error"]).To(ContainSubstring("uncertain"))
		Expect(ops[0]["cancellable"]).To(BeFalse())
		_, claimed, err := owner.ClaimLoadJob(ctx, job.TrackingKey, "retry")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse())
	})

	It("keeps an orphan visible as uncertain without mutating its durable job", func() {
		db := testutil.SetupTestDB()
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := registry.ClaimLoadJob(context.Background(), "orphan", "gone")
		Expect(err).NotTo(HaveOccurred())
		old := time.Now().Add(-96 * time.Hour)
		Expect(db.Model(&nodes.ModelLoadJob{}).Where("tracking_key = ?", job.TrackingKey).Updates(map[string]any{"state": nodes.LoadJobStateStaging, "last_progress": old, "started_at": old, "bytes_sent": 20, "total_bytes": 100}).Error).To(Succeed())
		router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{})
		router.StagingTracker().Start("orphan", "stale-local", 1)
		router.StagingTracker().UpdateFile("orphan", "weights", 1, 99, 100, "fast")
		e := recoveryOperationsServer(registry, router)
		ops := recoveryOperations(e)
		Expect(ops).To(HaveLen(1))
		Expect(ops[0]["error"]).To(ContainSubstring("uncertain"))
		Expect(ops[0]["phase"]).To(Equal("recovery"))
		Expect(ops[0]["job_id"]).To(Equal(job.Generation))
		Expect(ops[0]["cancellable"]).To(BeFalse())
		after, err := registry.GetLoadJob(context.Background(), "orphan")
		Expect(err).NotTo(HaveOccurred())
		Expect(after.State).To(Equal(nodes.LoadJobStateStaging))
		Expect(after.TerminalUntil).To(BeNil())
		Expect(after.LastProgress.Unix()).To(Equal(old.Unix()))
	})
	It("lets durable terminal state override both local and delayed mirrored progress", func() {
		db := testutil.SetupTestDB()
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := registry.ClaimLoadJob(context.Background(), "terminal", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.UpdateLoadJob(context.Background(), job.Ref(), nodes.LoadJobUpdate{State: nodes.LoadJobStateStaging})).To(Succeed())
		Expect(registry.FailLoadJob(context.Background(), job.Ref(), "remote work uncertain")).To(Succeed())
		for _, remote := range []bool{false, true} {
			router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{})
			if remote {
				router.StagingTracker().ApplyRemote(nodes.StagingProgressEvent{ModelID: "terminal", Status: &nodes.StagingStatus{ModelID: "terminal", Progress: 99}})
			} else {
				router.StagingTracker().Start("terminal", "stale", 1)
			}
			ops := recoveryOperations(recoveryOperationsServer(registry, router))
			Expect(ops).To(HaveLen(1))
			Expect(ops[0]["error"]).To(ContainSubstring("uncertain"))
			Expect(ops[0]["terminal"]).To(BeTrue())
			Expect(ops[0]["id"]).To(Equal("staging:terminal:" + job.Generation))
		}
	})
	It("merges only fresher progress from the same generation during a long live transfer", func() {
		db := testutil.SetupTestDB()
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := registry.ClaimLoadJob(context.Background(), "live", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.UpdateLoadJob(context.Background(), job.Ref(), nodes.LoadJobUpdate{State: nodes.LoadJobStateStaging, BytesSent: 20, TotalBytes: 100})).To(Succeed())
		Expect(db.Model(&nodes.ModelLoadJob{}).Where("tracking_key = ?", "live").Update("started_at", time.Now().Add(-96*time.Hour)).Error).To(Succeed())
		for _, generation := range []string{"old", job.Generation, "stale-same-generation"} {
			timestamp := time.Now()
			wireGeneration := generation
			if generation == "stale-same-generation" {
				wireGeneration = job.Generation
				timestamp = timestamp.Add(-time.Hour)
			}
			router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{})
			// JSON exercises the real broadcast schema, including generation metadata.
			payload, err := json.Marshal(map[string]any{"model_id": "live", "status": map[string]any{"model_id": "live", "generation": wireGeneration, "updated_at": timestamp, "bytes_sent": 80, "total_bytes": 100, "progress": 80}})
			Expect(err).NotTo(HaveOccurred())
			var event nodes.StagingProgressEvent
			Expect(json.Unmarshal(payload, &event)).To(Succeed())
			router.StagingTracker().ApplyRemote(event)
			ops := recoveryOperations(recoveryOperationsServer(registry, router))
			Expect(ops).To(HaveLen(1))
			expected := float64(20)
			if generation == job.Generation {
				expected = 80
			}
			Expect(ops[0]["currentBytes"]).To(Equal(expected))
			Expect(ops[0]["error"]).To(BeNil())
			Expect(ops[0]["id"]).To(Equal("staging:live:" + job.Generation))
		}
	})
	It("does not resurrect a completed generation from a delayed broadcast", func() {
		registry, err := nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		job, _, err := registry.ClaimLoadJob(context.Background(), "completed", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.DeleteLoadJob(context.Background(), job.Ref())).To(Succeed())
		router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{})
		router.StagingTracker().ApplyRemote(nodes.StagingProgressEvent{ModelID: job.TrackingKey, Status: &nodes.StagingStatus{ModelID: job.TrackingKey, Generation: job.Generation, UpdatedAt: time.Now(), Progress: 99}})
		Expect(recoveryOperations(recoveryOperationsServer(registry, router))).To(BeEmpty())
	})

	It("rejects synthetic staging cancellation before invoking gallery callbacks", func() {
		cfg := &config.ApplicationConfig{}
		svc := galleryop.NewGalleryService(cfg, nil)
		cache := galleryop.NewOpCache(svc)
		called := false
		svc.StoreCancellationActions("staging:model:generation", func() { called = true }, nil)
		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, cfg, svc, cache, applicationWithDistributedServices(nil, nil), func(next echo.HandlerFunc) echo.HandlerFunc { return next })
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/operations/staging:model:generation/cancel", nil))
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(called).To(BeFalse())
	})
})

func recoveryOperationsServer(registry *nodes.NodeRegistry, router *nodes.SmartRouter) *echo.Echo {
	cfg := &config.ApplicationConfig{}
	svc := galleryop.NewGalleryService(cfg, nil)
	e := echo.New()
	routes.RegisterUIAPIRoutes(e, nil, nil, cfg, svc, galleryop.NewOpCache(svc), applicationWithDistributedServices(registry, router), func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	return e
}
func recoveryOperations(e *echo.Echo) []map[string]any {
	GinkgoHelper()
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/operations", nil))
	Expect(rec.Code).To(Equal(http.StatusOK))
	var envelope struct {
		Operations []map[string]any `json:"operations"`
	}
	Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
	return envelope.Operations
}
