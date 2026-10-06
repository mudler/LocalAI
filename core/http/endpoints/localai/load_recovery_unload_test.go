package localai

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http/httptest"
	"strings"
)

type recoveryUnloadSender struct {
	nodes.NodeCommandSender
	calls int
}

func (s *recoveryUnloadSender) UnloadModelOnNode(string, string) error { s.calls++; return nil }
func (s *recoveryUnloadSender) StopBackend(string, string) error       { s.calls++; return nil }

var _ = Describe("Load recovery node unload", func() {
	It("does not broaden a different node target to a loading job", func() {
		r, err := nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		job, _, err := r.ClaimLoadJob(context.Background(), "m", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.UpdateLoadJob(context.Background(), job.Ref(), nodes.LoadJobUpdate{NodeID: "node-a"})).To(Succeed())
		sender := &recoveryUnloadSender{}
		e := echo.New()
		e.POST("/nodes/:id/unload", UnloadModelOnNodeEndpoint(sender, r))
		req := httptest.NewRequest("POST", "/nodes/node-b/unload", strings.NewReader(`{"model_name":"m"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(409))
		Expect(sender.calls).To(BeZero())
		current, err := r.GetLoadJob(context.Background(), "m")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.CancelRequested).To(BeFalse())
	})
})
