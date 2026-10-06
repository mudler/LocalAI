package localai

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"net/http/httptest"
	"strings"
)

var _ = Describe("Independent Task4 probes", func() {
	It("node target interleaving", func() {
		db := testutil.SetupTestDB()
		r, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		j, _, err := r.ClaimLoadJob(context.Background(), "review-node", "owner")
		Expect(err).NotTo(HaveOccurred())
		err = r.UpdateLoadJob(context.Background(), j.Ref(), nodes.LoadJobUpdate{NodeID: "node-a"})
		Expect(err).NotTo(HaveOccurred())
		fired := false
		name := "review_move"
		Expect(db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
			if fired || tx.Statement.Table != "model_load_jobs" {
				return
			}
			fired = true
			err := r.UpdateLoadJob(context.Background(), j.Ref(), nodes.LoadJobUpdate{NodeID: "node-b"})
			Expect(err).NotTo(HaveOccurred())
		})).To(Succeed())
		DeferCleanup(func() error { return db.Callback().Query().Remove(name) })
		e := echo.New()
		sender := &recoveryUnloadSender{}
		e.POST("/nodes/:id/unload", UnloadModelOnNodeEndpoint(sender, r))
		req := httptest.NewRequest("POST", "/nodes/node-a/unload", strings.NewReader(`{"model_name":"review-node"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		current, err := r.GetLoadJob(context.Background(), j.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(fired).To(BeTrue(), "interleaving not exercised")
		Expect(rec.Code).To(Equal(409))
		Expect(sender.calls).To(BeZero())
		Expect(current.NodeID).To(Equal("node-b"))
		Expect(current.TerminalUntil).To(BeNil())
		Expect(current.CancelRequested).To(BeFalse(), "node-a request canceled job now on node-b: HTTP %d body %s", rec.Code, rec.Body.String())
	})
})
