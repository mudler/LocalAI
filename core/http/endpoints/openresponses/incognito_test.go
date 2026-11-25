// SPDX-License-Identifier: MIT
package openresponses

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
)

var _ = Describe("a zero-retention Open Responses request", func() {
	It("refuses previous_response_id with a clear error instead of answering without the earlier turns", func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		req = req.WithContext(privacy.WithLevel(req.Context(), privacy.ZeroRetention))
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		input := &schema.OpenResponsesRequest{Model: "test-model", Input: "hello", PreviousResponseID: "resp_earlier", Context: req.Context()}
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "test-model"})
		handler := ResponsesEndpoint(nil, nil, templates.NewEvaluator(""), config.NewApplicationConfig(), nil)
		Expect(handler(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("zero-retention"))
		Expect(rec.Body.String()).To(ContainSubstring("previous_response_id"))
	})
})
