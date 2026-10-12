// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/natsauth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
)

var _ = Describe("Worker status authentication", func() {
	It("rejects missing and invalid tokens before accessing registry", func() {
		e := echo.New()
		RegisterNodeSelfServiceRoutes(e, &nodes.NodeRegistry{}, "secret", false, nil, "", natsauth.Config{})
		for _, token := range []string{"", "Bearer wrong"} {
			req := httptest.NewRequest(http.MethodGet, "/api/node/id/status", nil)
			req.Header.Set("Authorization", token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		}
	})
	It("does not register approval route on unauthenticated deployments", func() {
		e := echo.New()
		RegisterNodeSelfServiceRoutes(e, &nodes.NodeRegistry{}, "", false, nil, "", natsauth.Config{})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/node/id/status", nil))
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})
})
