// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// No page is built in today, so these tests make up two built-in pages to check
// how the routes filter and keep pins.
func madeUpPages() {
	DeferCleanup(builtin.SetCatalogForTest([]builtin.Entry{
		{ID: "guide", Kind: builtin.KindPage, TitleKey: "items.chat", Icon: "chat", MainPath: "/app/chat"},
		{ID: "help", Kind: builtin.KindPage, TitleKey: "items.models", Icon: "boxes", MainPath: "/app/models"},
	}))
}

// pinsApp builds the two routes over a store. With seeded set, the upgrade seed
// has run for the local owner, as it does at start in a deployment that
// existed before this release.
func pinsApp(seeded bool) *echo.Echo {
	madeUpPages()
	store, err := pins.NewFileStore("")
	Expect(err).ToNot(HaveOccurred())
	if seeded {
		_, err := store.SeedExisting(context.Background(), []string{auth.AddonLocalOwnerID}, []string{"guide", "help"})
		Expect(err).ToNot(HaveOccurred())
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

var _ = Describe("the pins routes", func() {
	It("gives no pins to an account created after the upgrade", func() {
		e := pinsApp(false)
		code, got := call(e, http.MethodGet, "")
		Expect(code).To(Equal(200), "no seed has run for this account, so it has no pins")
		Expect(got).To(BeEmpty())
	})

	It("round-trips pins for the local owner", func() {
		e := pinsApp(true)
		code, got := call(e, http.MethodGet, "")
		Expect(code).To(Equal(200))
		Expect(got).To(Equal([]string{"guide", "help"}), "seed")
		code, got = call(e, http.MethodPut, `["help","guide"]`)
		Expect(code).To(Equal(200))
		Expect(got).To(HaveLen(2))
		Expect(got[0]).To(Equal("help"), "put")
		_, got = call(e, http.MethodGet, "")
		Expect(got).To(HaveLen(2))
		Expect(got[0]).To(Equal("help"), "round trip")
	})

	It("drops IDs the caller cannot see", func() {
		e := pinsApp(false)
		_, got := call(e, http.MethodPut, `["guide","not-installed"]`)
		Expect(got).To(Equal([]string{"guide"}), "an unknown id must be dropped without a hint")
	})

	DescribeTable("rejects a body that is not a list of strings",
		func(body string) {
			e := pinsApp(false)
			code, _ := call(e, http.MethodPut, body)
			Expect(code).To(Equal(400), "body %q", body)
		},
		Entry("an object", `{"a":1}`),
		Entry("numbers", `[1,2]`),
		Entry("not JSON", `not json`),
		Entry("empty", ``),
		Entry("null", `null`),
	)

	It("rejects a list over the hard limit", func() {
		e := pinsApp(false)
		items := make([]string, pins.HardLimit+1)
		for i := range items {
			items[i] = `"guide"`
		}
		code, _ := call(e, http.MethodPut, "["+strings.Join(items, ",")+"]")
		Expect(code).To(Equal(400))
	})

	It("does not show Studio and Talk as pins without the add-ons", func() {
		// Studio and Talk are packaged add-ons: the routes list them only for a
		// caller whose owner lists them as installed, never as built-in pages.
		e := pinsApp(false)
		_, got := call(e, http.MethodPut, `["studio","talk","guide"]`)
		Expect(got).To(Equal([]string{"guide"}), "studio and talk are not built in, so they are dropped")
	})
})
