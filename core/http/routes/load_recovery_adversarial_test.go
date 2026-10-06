package routes_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"
)

var _ = Describe("Load recovery adversarial", func() {
	It("renders uncertainty as well as a real RPC failure to existing consumers", func() {
		db := testutil.SetupTestDB()
		r, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		j, _, err := r.ClaimLoadJob(context.Background(), "rpc-error", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.FailLoadJob(context.Background(), j.Ref(), "context deadline exceeded")).To(Succeed())
		ops := recoveryOperations(recoveryOperationsServer(r, nodes.NewSmartRouter(r, nodes.SmartRouterOptions{})))
		Expect(ops).To(HaveLen(1))
		Expect(ops[0]["error"]).To(ContainSubstring("context deadline exceeded"))
		Expect(ops[0]["work_uncertain"]).To(BeTrue())
		Expect(ops[0]["error"]).To(ContainSubstring("uncertain"), "unchanged React only renders error, not work_uncertain")
	})
	It("does not return successful empty list on durable read failure to a replica with no mirror", func() {
		db := testutil.SetupTestDB()
		r, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		j, _, err := r.ClaimLoadJob(context.Background(), "db-error", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.UpdateLoadJob(context.Background(), j.Ref(), nodes.LoadJobUpdate{State: nodes.LoadJobStateStaging, BytesSent: 20, TotalBytes: 100})).To(Succeed())
		e := recoveryOperationsServer(r, nodes.NewSmartRouter(r, nodes.SmartRouterOptions{}))
		Expect(recoveryOperations(e)).To(HaveLen(1))
		Expect(db.Callback().Query().Before("gorm:query").Register("review-read-error", func(tx *gorm.DB) {
			if tx.Statement.Table == "model_load_jobs" {
				tx.AddError(errors.New("review injected read failure"))
			}
		})).To(Succeed())
		defer db.Callback().Query().Remove("review-read-error")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/operations", nil))
		GinkgoWriter.Printf("durable read failure HTTP status=%d body=%s\n", rec.Code, rec.Body.String())
		Expect(rec.Code).To(BeNumerically(">=", 500), "200 empty operations triggers OperationsBar staged-success branch")
	})
	It("preserves current durable generation counts with clock-skewed NATS generations", func() {
		db := testutil.SetupTestDB()
		r, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		j, _, err := r.ClaimLoadJob(context.Background(), "clock", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.UpdateLoadJob(context.Background(), j.Ref(), nodes.LoadJobUpdate{State: nodes.LoadJobStateStaging, BytesSent: 20, TotalBytes: 100})).To(Succeed())
		router := nodes.NewSmartRouter(r, nodes.SmartRouterOptions{})
		nc, err := messaging.New(recoveryNATS())
		Expect(err).NotTo(HaveOccurred())
		defer nc.Close()
		sub, err := router.StagingTracker().SubscribeBroadcasts(nc)
		Expect(err).NotTo(HaveOccurred())
		defer sub.Unsubscribe()
		now := time.Now()
		send := func(gen string, started time.Time, bytes int64) {
			Expect(nc.Publish(messaging.SubjectStagingProgress("clock"), nodes.StagingProgressEvent{ModelID: "clock", Status: &nodes.StagingStatus{ModelID: "clock", Generation: gen, StartedAt: started, UpdatedAt: now.Add(time.Second), BytesSent: bytes, TotalBytes: 100, Progress: float64(bytes)}})).To(Succeed())
		}
		send("old-fast-clock", now.Add(time.Hour), 10)
		Eventually(func() *nodes.StagingStatus { return router.StagingTracker().Get("clock") }).ShouldNot(BeNil())
		send(j.Generation, now, 80)
		send("old-fast-clock", now.Add(2*time.Hour), 5)
		// A marker on the same subscription proves both preceding messages were processed.
		Expect(nc.Publish(messaging.SubjectStagingProgress("marker"), nodes.StagingProgressEvent{ModelID: "marker", Status: &nodes.StagingStatus{ModelID: "marker"}})).To(Succeed())
		Eventually(func() *nodes.StagingStatus { return router.StagingTracker().Get("marker") }).ShouldNot(BeNil())
		ops := recoveryOperations(recoveryOperationsServer(r, router))
		var op map[string]any
		for _, v := range ops {
			if v["name"] == "clock" {
				op = v
			}
		}
		Expect(op).NotTo(BeNil())
		Expect(op["currentBytes"]).To(Equal(float64(80)), "durable identity, not cross-owner wall clocks, determines current generation")
	})
	It("keeps terminal durable failure authoritative through real NATS reorder and stale completion", func() {
		db := testutil.SetupTestDB()
		r, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		j, _, err := r.ClaimLoadJob(context.Background(), "reorder", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.FailLoadJob(context.Background(), j.Ref(), "remote work uncertain")).To(Succeed())
		router := nodes.NewSmartRouter(r, nodes.SmartRouterOptions{})
		nc, err := messaging.New(recoveryNATS())
		Expect(err).NotTo(HaveOccurred())
		defer nc.Close()
		sub, err := router.StagingTracker().SubscribeBroadcasts(nc)
		Expect(err).NotTo(HaveOccurred())
		defer sub.Unsubscribe()
		for _, gen := range []string{j.Generation, "older", ""} {
			Expect(nc.Publish(messaging.SubjectStagingProgress("reorder"), nodes.StagingProgressEvent{ModelID: "reorder", Generation: gen, Done: true})).To(Succeed())
			Expect(nc.Publish(messaging.SubjectStagingProgress("reorder"), nodes.StagingProgressEvent{ModelID: "reorder", Status: &nodes.StagingStatus{ModelID: "reorder", Generation: gen, UpdatedAt: time.Now().Add(time.Hour), Progress: 99}})).To(Succeed())
		}
		Expect(nc.Publish(messaging.SubjectStagingProgress("marker"), nodes.StagingProgressEvent{ModelID: "marker", Status: &nodes.StagingStatus{ModelID: "marker", Generation: "marker"}})).To(Succeed())
		Eventually(func() *nodes.StagingStatus { return router.StagingTracker().Get("marker") }).ShouldNot(BeNil())
		ops := recoveryOperations(recoveryOperationsServer(r, router))
		Expect(ops).To(HaveLen(1))
		Expect(ops[0]["job_id"]).To(Equal(j.Generation))
		Expect(ops[0]["error"]).To(ContainSubstring("uncertain"))
		Expect(ops[0]["currentBytes"]).To(Equal(float64(0)))
	})
	It("rejects all synthetic gallery actions and keeps failed quarantine", func() {
		db := testutil.SetupTestDB()
		r, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		j, _, err := r.ClaimLoadJob(context.Background(), "guard", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.FailLoadJob(context.Background(), j.Ref(), "uncertain")).To(Succeed())
		e := recoveryOperationsServer(r, nil)
		for _, action := range []string{"cancel", "pause", "dismiss"} {
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/operations/staging:guard:"+j.Generation+"/"+action, nil))
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		}
		ops := recoveryOperations(e)
		Expect(ops).To(HaveLen(1))
		Expect(ops[0]["cancellable"]).To(BeFalse())
		after, err := r.GetLoadJob(context.Background(), "guard")
		Expect(err).NotTo(HaveOccurred())
		Expect(after.WorkUncertain).To(BeTrue())
	})
})

// Each transport regression owns a disposable local broker.
func recoveryNATS() string {
	GinkgoHelper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{Image: "nats:2-alpine", ExposedPorts: []string{"4222/tcp"}, WaitingFor: wait.ForListeningPort("4222/tcp")}, Started: true,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(c.Terminate(ctx)).To(Succeed()) })
	host, err := c.Host(ctx)
	Expect(err).NotTo(HaveOccurred())
	port, err := c.MappedPort(ctx, "4222/tcp")
	Expect(err).NotTo(HaveOccurred())
	return fmt.Sprintf("nats://%s:%s", host, port.Port())
}
