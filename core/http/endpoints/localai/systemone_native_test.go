package localai

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"net/http/httptest"
)

var _ = Describe("SystemOne native response handling", func() {
	It("maps backend request and capability errors without changing unknown failures", func() {
		Expect(systemOneBackendStatus(status.Error(codes.InvalidArgument, "bad question"))).To(Equal(http.StatusBadRequest))
		Expect(systemOneBackendStatus(status.Error(codes.Unimplemented, "no decision metadata"))).To(Equal(http.StatusNotImplemented))
		Expect(systemOneBackendStatus(status.Error(codes.Internal, "failed"))).To(Equal(http.StatusInternalServerError))
	})
	It("stamps explicit zeros but not missing usage", func() {
		for _, body := range []string{`{"usage":{"input_tokens":0,"output_tokens":0}}`, `{"usage":{"input_tokens":12,"output_tokens":0}}`} {
			c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/systemone", nil), httptest.NewRecorder())
			Expect(stampSystemOneUsage(c, "decision", body)).To(Succeed())
			Expect(c.Get(middleware.ContextKeyCompletionTokens)).To(Equal(int64(0)))
			Expect(c.Get(middleware.ContextKeyResponseModel)).To(Equal("decision"))
		}
		for _, body := range []string{`{}`, `{"usage":{}}`} {
			c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/systemone", nil), httptest.NewRecorder())
			Expect(stampSystemOneUsage(c, "decision", body)).To(Succeed())
			Expect(c.Get(middleware.ContextKeyPromptTokens)).To(BeNil())
		}
	})
	It("rejects negative or invalid usage instead of recording it", func() {
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/systemone", nil), httptest.NewRecorder())
		Expect(stampSystemOneUsage(c, "decision", `{"usage":{"input_tokens":-1,"output_tokens":0}}`)).NotTo(Succeed())
		Expect(c.Get(middleware.ContextKeyPromptTokens)).To(BeNil())
	})
})

type decisionUsageCapture struct{ records []*auth.UsageRecord }

func (b *decisionUsageCapture) Record(_ context.Context, r *auth.UsageRecord) error {
	b.records = append(b.records, r)
	return nil
}
func (*decisionUsageCapture) Aggregate(context.Context, billing.AggregateQuery) ([]auth.UsageBucket, error) {
	return nil, nil
}
func (*decisionUsageCapture) Close() error { return nil }

var _ = Describe("SystemOne native route accounting", func() {
	DescribeTable("records exactly once and never fabricates output", func(body string, wantStatus, wantRecords int) {
		capture := &decisionUsageCapture{}
		e := echo.New()
		e.POST("/v1/systemone", func(c echo.Context) error {
			return respondSystemOne(c, "decision", func(context.Context) (string, error) { return body, nil })
		}, middleware.UsageMiddleware(billing.NewRecorder(capture), &auth.User{ID: "test"}))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", nil))
		Expect(w.Code).To(Equal(wantStatus))
		Expect(capture.records).To(HaveLen(wantRecords))
		if wantRecords > 0 {
			Expect(capture.records[0].CompletionTokens).To(Equal(int64(0)))
		}
	},
		Entry("positive input zero output", `{"usage":{"input_tokens":12,"output_tokens":0}}`, 200, 1),
		Entry("explicit zeros", `{"usage":{"input_tokens":0,"output_tokens":0}}`, 200, 1),
		Entry("missing", `{}`, 200, 0),
		Entry("incomplete", `{"usage":{"input_tokens":12}}`, 500, 0),
		Entry("negative", `{"usage":{"input_tokens":-1,"output_tokens":0}}`, 500, 0),
	)
	DescribeTable("maps RPC errors at the HTTP route", func(code codes.Code, want int) {
		e := echo.New()
		e.POST("/v1/systemone", func(c echo.Context) error {
			return respondSystemOne(c, "decision", func(context.Context) (string, error) { return "", status.Error(code, "backend error") })
		})
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", nil))
		Expect(w.Code).To(Equal(want))
	}, Entry("invalid", codes.InvalidArgument, 400), Entry("unsupported", codes.Unimplemented, 501))
})
