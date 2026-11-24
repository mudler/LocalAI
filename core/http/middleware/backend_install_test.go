package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RequireBackendInstallEnabled", func() {
	run := func(disabled bool) (*httptest.ResponseRecorder, bool) {
		called := false
		handler := func(c echo.Context) error { called = true; return c.NoContent(http.StatusOK) }
		req := httptest.NewRequest(http.MethodPost, "/backends/apply", nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		err := RequireBackendInstallEnabled(&config.ApplicationConfig{DisableBackendInstall: disabled})(handler)(c)
		Expect(err).NotTo(HaveOccurred())
		return rec, called
	}

	It("passes requests through when installs are allowed", func() {
		rec, called := run(false)
		Expect(called).To(BeTrue())
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("answers 403 with a stable code when installs are disabled", func() {
		rec, called := run(true)
		Expect(called).To(BeFalse())
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body["code"]).To(Equal("backend_install_disabled"))
		Expect(body["error"]).To(ContainSubstring("disable-backend-install"))
	})
})
