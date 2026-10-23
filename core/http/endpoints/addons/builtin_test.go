// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func listBuiltin() (*httptest.ResponseRecorder, []string) {
	e := echo.New()
	e.GET("/api/addons/builtin", ListBuiltin(nil))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/addons/builtin", nil))
	Expect(rec.Code).To(Equal(http.StatusOK))
	var body struct {
		Builtin []struct {
			ID string `json:"id"`
		} `json:"builtin"`
	}
	Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
	ids := []string{}
	for _, e := range body.Builtin {
		ids = append(ids, e.ID)
	}
	return rec, ids
}

var _ = Describe("ListBuiltin", func() {
	It("is empty because Studio and Talk are packaged add-ons", func() {
		rec, ids := listBuiltin()
		Expect(ids).To(BeEmpty(), "no page is built in")
		Expect(rec.Header().Get("Cache-Control")).To(Equal("private, no-store"),
			"the list depends on the caller and must not be cached")
	})

	It("lists every entry without an account database", func() {
		DeferCleanup(builtin.SetCatalogForTest([]builtin.Entry{
			{ID: "guide", Kind: builtin.KindPage, TitleKey: "items.chat", Icon: "chat", MainPath: "/app/chat"},
			{ID: "notes-page", Kind: builtin.KindPage, TitleKey: "items.models", Icon: "boxes", MainPath: "/app/models"},
		}))
		rec, ids := listBuiltin()
		Expect(ids).To(Equal([]string{"guide", "notes-page"}), "unexpected body %s", rec.Body.String())
		Expect(rec.Header().Get("Cache-Control")).To(Equal("private, no-store"),
			"the list depends on the caller and must not be cached")
	})
})
