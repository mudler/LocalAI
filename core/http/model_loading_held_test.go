package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// A request for a model held by a failed load gets 503 with a Retry-After that
// says when the hold ends, and the real cause. It never gets a bare 500.
var _ = Describe("A model held by a failed load", func() {
	It("answers 503 with Retry-After, the cause and the job", func() {
		stop := time.Now().Add(120 * time.Second)
		job := &nodes.ModelLoadJob{TrackingKey: "held-model", Generation: "gen-1", State: nodes.LoadJobStateFailed,
			LastError: "worker out of disk", StopDeadline: &stop}
		err := fmt.Errorf("routing: %w", nodes.NewLoadHeldError(job))

		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), rec)

		Expect(respondModelLoading(err, c)).To(BeTrue(), "a held model must be answered as a loading error")
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(rec.Header().Get("Retry-After")).To(MatchRegexp(`^(1[0-9][0-9]|2[0-9][0-9])$`), "the seconds until the hold ends")
		var body schema.ModelLoadingResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Error).ToNot(BeNil())
		Expect(body.Loading).ToNot(BeNil())
		Expect(body.Loading.LastError).To(Equal("worker out of disk"))
		Expect(body.Loading.JobID).To(Equal("gen-1"))
	})
})
