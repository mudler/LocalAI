package localai_test

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http/httptest"
	"strings"
)

var _ = Describe("Load recovery cancel body", func() {
	It("rejects missing generation, unknown fields and trailing JSON", func() {
		e := echo.New()
		e.POST("/api/models/:id/load-cancel", localai.ModelLoadCancelEndpoint(func() *nodes.LoadRecoveryService { return nil }, nil))
		for _, body := range []string{`{}`, `{"job_id":""}`, `{"job_id":"x","other":true}`, `{"job_id":"x"} {}`, `null`} {
			req := httptest.NewRequest("POST", "/api/models/m/load-cancel", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(400), body)
		}
	})
})

var _ = Describe("Load recovery cancel responses", func() {
	It("reports pending durably across registries and never confirms absence", func() {
		db := testutil.SetupTestDB()
		a, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		b, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := a.ClaimLoadJob(context.Background(), "m", "owner")
		Expect(err).NotTo(HaveOccurred())
		e := echo.New()
		e.POST("/api/models/:id/load-cancel", localai.ModelLoadCancelEndpoint(func() *nodes.LoadRecoveryService { return &nodes.LoadRecoveryService{Registry: b} }, nil))
		for _, tc := range []struct {
			model, id string
			code      int
		}{{"m", job.Generation, 202}, {"m", job.Generation, 202}, {"m", "wrong", 409}, {"unknown", "missing", 404}} {
			req := httptest.NewRequest("POST", "/api/models/"+tc.model+"/load-cancel", strings.NewReader(`{"job_id":"`+tc.id+`"}`))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(tc.code))
		}
		current, err := a.GetLoadJob(context.Background(), "m")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.CancelRequested).To(BeTrue())
		Expect(current.WorkUncertain).To(BeTrue())
	})
})
