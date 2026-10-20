// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestListBuiltinWithoutAnAccountDatabaseListsEveryEntry(t *testing.T) {
	e := echo.New()
	e.GET("/api/addons/builtin", ListBuiltin(nil))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/addons/builtin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Builtin []struct {
			ID string `json:"id"`
		} `json:"builtin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Builtin) != 2 || body.Builtin[0].ID != "studio" || body.Builtin[1].ID != "talk" {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("the list depends on the caller and must not be cached, got %q", rec.Header().Get("Cache-Control"))
	}
}
