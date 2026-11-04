// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a read the owner did not answer", func() {
	answer := func(e error) (int, AddonErrorResponse) {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/addons/apps", nil), rec)
		Expect(resultError(c, e)).To(Succeed())
		var out AddonErrorResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		return rec.Code, out
	}

	It("is a typed owner_busy 503 when the owner timed out", func() {
		code, body := answer(&gateway.OwnerTimeout{After: 8 * time.Second})
		Expect(code).To(Equal(503))
		Expect(body.Error.Type).To(Equal("owner_busy"))
		Expect(body.Error.Message).To(ContainSubstring("busy"))
	})

	It("keeps the plain unavailable answer for an owner that cannot be reached", func() {
		code, body := answer(errors.Join(gateway.ErrUnavailable))
		Expect(code).To(Equal(503))
		Expect(body.Error.Type).To(Equal("addon_error"))
	})

	It("tells the pins routes apart too", func() {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/addons/pins", nil), rec)
		Expect(ownerNotAnswering(c, &gateway.OwnerTimeout{})).To(Succeed())
		Expect(rec.Code).To(Equal(503))
		Expect(rec.Body.String()).To(ContainSubstring("owner_busy"))
		rec = httptest.NewRecorder()
		c = echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/addons/pins", nil), rec)
		Expect(ownerNotAnswering(c, gateway.ErrUnavailable)).To(Succeed())
		Expect(rec.Body.String()).To(ContainSubstring("owner_unavailable"))
	})
})
