// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
	"github.com/mudler/LocalAI/core/services/addons/pins"
)

// No page is built in today, so these tests make up two built-in pages to check
// how the routes filter and keep pins.
func madeUpPages(t *testing.T) {
	t.Helper()
	t.Cleanup(builtin.SetCatalogForTest([]builtin.Entry{
		{ID: "guide", Kind: builtin.KindPage, TitleKey: "items.chat", Icon: "chat", MainPath: "/app/chat"},
		{ID: "help", Kind: builtin.KindPage, TitleKey: "items.models", Icon: "boxes", MainPath: "/app/models"},
	}))
}

// pinsApp builds the two routes over a store. With seeded set, the upgrade seed
// has run for the local owner, as it does at start in a deployment that
// existed before this release.
func pinsApp(t *testing.T, seeded bool) *echo.Echo {
	madeUpPages(t)
	store, err := pins.NewFileStore("")
	if err != nil {
		t.Fatal(err)
	}
	if seeded {
		if _, err := store.SeedExisting(context.Background(), []string{auth.AddonLocalOwnerID}, []string{"guide", "help"}); err != nil {
			t.Fatal(err)
		}
	}
	svc := &pins.Service{Store: store}
	e := echo.New()
	e.GET("/api/addons/pins", GetPins(nil, nil, svc))
	e.PUT("/api/addons/pins", PutPins(nil, nil, svc))
	return e
}

func call(e *echo.Echo, method, body string) (int, []string) {
	req := httptest.NewRequest(method, "/api/addons/pins", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var out []string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestAnAccountCreatedAfterTheUpgradeGetsNoPins(t *testing.T) {
	e := pinsApp(t, false)
	code, got := call(e, http.MethodGet, "")
	if code != 200 || len(got) != 0 {
		t.Fatalf("no seed has run for this account, so it has no pins: %d %v", code, got)
	}
}

func TestPinsRoundTripForTheLocalOwner(t *testing.T) {
	e := pinsApp(t, true)
	code, got := call(e, http.MethodGet, "")
	if code != 200 || len(got) != 2 || got[0] != "guide" || got[1] != "help" {
		t.Fatalf("seed: %d %v", code, got)
	}
	code, got = call(e, http.MethodPut, `["help","guide"]`)
	if code != 200 || got[0] != "help" {
		t.Fatalf("put: %d %v", code, got)
	}
	_, got = call(e, http.MethodGet, "")
	if len(got) != 2 || got[0] != "help" {
		t.Fatalf("round trip: %v", got)
	}
}

func TestPutDropsIDsTheCallerCannotSee(t *testing.T) {
	e := pinsApp(t, false)
	_, got := call(e, http.MethodPut, `["guide","not-installed"]`)
	if len(got) != 1 || got[0] != "guide" {
		t.Fatalf("an unknown id must be dropped without a hint: %v", got)
	}
}

func TestPutRejectsABodyThatIsNotAListOfStrings(t *testing.T) {
	e := pinsApp(t, false)
	for _, body := range []string{`{"a":1}`, `[1,2]`, `not json`, ``, `null`} {
		if code, _ := call(e, http.MethodPut, body); code != 400 {
			t.Fatalf("body %q: want 400, got %d", body, code)
		}
	}
}

func TestPutRejectsAListOverTheHardLimit(t *testing.T) {
	e := pinsApp(t, false)
	items := make([]string, pins.HardLimit+1)
	for i := range items {
		items[i] = `"guide"`
	}
	if code, _ := call(e, http.MethodPut, "["+strings.Join(items, ",")+"]"); code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
}

func TestStudioAndTalkAreNotVisiblePinsWithoutTheAddOns(t *testing.T) {
	// Studio and Talk are packaged add-ons: the routes list them only for a
	// caller whose owner lists them as installed, never as built-in pages.
	e := pinsApp(t, false)
	_, got := call(e, http.MethodPut, `["studio","talk","guide"]`)
	if len(got) != 1 || got[0] != "guide" {
		t.Fatalf("studio and talk are not built in, so they are dropped: %v", got)
	}
}
