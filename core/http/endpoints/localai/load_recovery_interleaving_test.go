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
		t := GinkgoT()
		db := testutil.SetupTestDB()
		r, err := nodes.NewNodeRegistry(db)
		if err != nil {
			t.Fatal(err)
		}
		j, _, err := r.ClaimLoadJob(context.Background(), "review-node", "owner")
		if err != nil {
			t.Fatal(err)
		}
		if err = r.UpdateLoadJob(context.Background(), j.Ref(), nodes.LoadJobUpdate{NodeID: "node-a"}); err != nil {
			t.Fatal(err)
		}
		fired := false
		name := "review_move"
		db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
			if fired || tx.Statement.Table != "model_load_jobs" {
				return
			}
			fired = true
			if err := r.UpdateLoadJob(context.Background(), j.Ref(), nodes.LoadJobUpdate{NodeID: "node-b"}); err != nil {
				t.Fatal(err)
			}
		})
		defer db.Callback().Query().Remove(name)
		e := echo.New()
		sender := &recoveryUnloadSender{}
		e.POST("/nodes/:id/unload", UnloadModelOnNodeEndpoint(sender, r))
		req := httptest.NewRequest("POST", "/nodes/node-a/unload", strings.NewReader(`{"model_name":"review-node"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		current, err := r.GetLoadJob(context.Background(), j.TrackingKey)
		if err != nil {
			t.Fatal(err)
		}
		if !fired {
			t.Fatal("interleaving not exercised")
		}
		Expect(rec.Code).To(Equal(409))
		Expect(sender.calls).To(BeZero())
		Expect(current.NodeID).To(Equal("node-b"))
		Expect(current.TerminalUntil).To(BeNil())
		if current.CancelRequested {
			t.Fatalf("node-a request canceled job now on node-b: HTTP %d body %s", rec.Code, rec.Body.String())
		}
	})
})
