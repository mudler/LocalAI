// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
)

func listBuiltin(t *testing.T) (*httptest.ResponseRecorder, []string) {
	t.Helper()
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
	ids := []string{}
	for _, e := range body.Builtin {
		ids = append(ids, e.ID)
	}
	return rec, ids
}

func TestListBuiltinIsEmptyBecauseStudioAndTalkArePackagedAddOns(t *testing.T) {
	rec, ids := listBuiltin(t)
	if len(ids) != 0 {
		t.Fatalf("no page is built in, got %v", ids)
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("the list depends on the caller and must not be cached, got %q", rec.Header().Get("Cache-Control"))
	}
}

func TestListBuiltinWithoutAnAccountDatabaseListsEveryEntry(t *testing.T) {
	defer builtin.SetCatalogForTest([]builtin.Entry{
		{ID: "guide", Kind: builtin.KindPage, TitleKey: "items.chat", Icon: "chat", MainPath: "/app/chat"},
		{ID: "notes-page", Kind: builtin.KindPage, TitleKey: "items.models", Icon: "boxes", MainPath: "/app/models"},
	})()
	rec, ids := listBuiltin(t)
	if len(ids) != 2 || ids[0] != "guide" || ids[1] != "notes-page" {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("the list depends on the caller and must not be cached, got %q", rec.Header().Get("Cache-Control"))
	}
}
