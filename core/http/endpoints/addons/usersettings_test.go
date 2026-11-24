// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/usersettings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var memoryDecl = []contract.UserSetting{{Key: "save-mode", Values: []string{"ask", "auto", "never"}, Default: "ask"}}

type failingSettings struct{ usersettings.Store }

func (failingSettings) Get(context.Context, string, string) (map[string]string, error) {
	return nil, errors.New("down")
}

func userSettingsApp(store usersettings.Store, decl UserSettingsDeclarer) *echo.Echo {
	e := echo.New()
	mode := WithOwnership(auth.AddonModeLocalOwner)
	e.GET("/api/addons/:id/user-settings", GetUserSettings(decl, store, nil, mode))
	e.PUT("/api/addons/:id/user-settings", PutUserSetting(decl, store, nil, mode))
	return e
}

func declares(id string, list []contract.UserSetting) UserSettingsDeclarer {
	return func(_ context.Context, _ contract.Actor, addon string) ([]contract.UserSetting, bool, error) {
		if addon != id {
			return nil, false, nil
		}
		return list, true, nil
	}
}

// browserPut is the request of the person's own page.
func browserPut(id, body string, mod ...func(*http.Request)) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/api/addons/"+id+"/user-settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set(ConfirmHeader, id)
	for _, m := range mod {
		m(req)
	}
	return req
}

func serve(e *echo.Echo, req *http.Request) (int, UserSettingsResponse, string) {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var out UserSettingsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func getSettings(e *echo.Echo, id string) (int, UserSettingsResponse) {
	code, out, _ := serve(e, httptest.NewRequest(http.MethodGet, "/api/addons/"+id+"/user-settings", nil))
	return code, out
}

var _ = Describe("the user settings routes", func() {
	var (
		store usersettings.Store
		e     *echo.Echo
	)
	BeforeEach(func() {
		s, err := usersettings.NewFileStore("")
		Expect(err).ToNot(HaveOccurred())
		store = s
		e = userSettingsApp(store, declares("memory", memoryDecl))
	})

	It("gives the default to a person who has not chosen", func() {
		code, out := getSettings(e, "memory")
		Expect(code).To(Equal(200))
		Expect(out.Settings).To(Equal([]UserSettingView{{Key: "save-mode", Values: []string{"ask", "auto", "never"}, Default: "ask", Value: "ask"}}))
	})

	It("stores a declared value and reads it back", func() {
		code, out, _ := serve(e, browserPut("memory", `{"key":"save-mode","value":"auto"}`))
		Expect(code).To(Equal(200))
		Expect(out.Settings[0].Value).To(Equal("auto"))
		_, out = getSettings(e, "memory")
		Expect(out.Settings[0].Value).To(Equal("auto"))
		stored, _ := store.Get(context.Background(), auth.AddonLocalOwnerID, "memory")
		Expect(stored).To(Equal(map[string]string{"save-mode": "auto"}), "the host keeps it, under the person")
	})

	It("repeats a write with the same result", func() {
		for range 2 {
			code, out, _ := serve(e, browserPut("memory", `{"key":"save-mode","value":"never"}`))
			Expect(code).To(Equal(200))
			Expect(out.Settings[0].Value).To(Equal("never"))
		}
	})

	DescribeTable("refuses what the manifest does not declare, and stores nothing",
		func(body string) {
			code, _, _ := serve(e, browserPut("memory", body))
			Expect(code).To(Equal(400), body)
			stored, _ := store.Get(context.Background(), auth.AddonLocalOwnerID, "memory")
			Expect(stored).To(BeEmpty())
		},
		Entry("a value that is not allowed", `{"key":"save-mode","value":"always"}`),
		Entry("an empty value", `{"key":"save-mode","value":""}`),
		Entry("a key nobody declared", `{"key":"other","value":"auto"}`),
		Entry("an extra field", `{"key":"save-mode","value":"auto","x":1}`),
		Entry("not JSON", `auto`),
		Entry("empty", ``),
	)

	It("answers 404 for an add-on that is not there, with the same words for a bad name", func() {
		code, _ := getSettings(e, "nothing")
		Expect(code).To(Equal(404))
		code, _, _ = serve(e, browserPut("nothing", `{"key":"save-mode","value":"auto"}`))
		Expect(code).To(Equal(404))
		code, _ = getSettings(e, "Bad..Name")
		Expect(code).To(Equal(404))
	})

	It("gives an empty list to an add-on that declares none", func() {
		e = userSettingsApp(store, declares("memory", nil))
		code, out := getSettings(e, "memory")
		Expect(code).To(Equal(200))
		Expect(out.Settings).To(BeEmpty())
		code, _, _ = serve(e, browserPut("memory", `{"key":"save-mode","value":"auto"}`))
		Expect(code).To(Equal(400))
	})

	DescribeTable("takes a write only from the person's own browser session",
		func(mod func(*http.Request)) {
			code, _, body := serve(e, browserPut("memory", `{"key":"save-mode","value":"auto"}`, mod))
			Expect(code).To(Equal(403), body)
			Expect(body).To(ContainSubstring("not_human_surface"))
			stored, _ := store.Get(context.Background(), auth.AddonLocalOwnerID, "memory")
			Expect(stored).To(BeEmpty())
		},
		Entry("an API key header", func(r *http.Request) { r.Header.Set("Authorization", "Bearer sk-x") }),
		Entry("an x-api-key header", func(r *http.Request) { r.Header.Set("X-Api-Key", "k") }),
		Entry("no confirmation header", func(r *http.Request) { r.Header.Del(ConfirmHeader) }),
		Entry("the confirmation of another add-on", func(r *http.Request) { r.Header.Set(ConfirmHeader, "other") }),
		Entry("a cross-site request", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }),
		Entry("a navigation instead of a fetch", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "document") }),
		Entry("a tool call", func(r *http.Request) { r.Header.Set("X-LocalAI-Tool-Call", "1") }),
	)

	It("answers 503 and does not guess when the store is down", func() {
		e = userSettingsApp(failingSettings{store}, declares("memory", memoryDecl))
		code, _ := getSettings(e, "memory")
		Expect(code).To(Equal(503))
	})

	It("answers 503 when the owner cannot say what the add-on declares", func() {
		e = userSettingsApp(store, func(context.Context, contract.Actor, string) ([]contract.UserSetting, bool, error) {
			return nil, false, errors.New("owner down")
		})
		code, _ := getSettings(e, "memory")
		Expect(code).To(Equal(503))
		code, _, _ = serve(e, browserPut("memory", `{"key":"save-mode","value":"auto"}`))
		Expect(code).To(Equal(503))
	})

	It("treats a stored value that the manifest no longer allows as the default", func() {
		Expect(store.Set(context.Background(), auth.AddonLocalOwnerID, "memory", "save-mode", "retired")).To(Succeed())
		_, out := getSettings(e, "memory")
		Expect(out.Settings[0].Value).To(Equal("ask"))
	})

	It("keeps one person's choice from another", func() {
		Expect(store.Set(context.Background(), "someone-else", "memory", "save-mode", "auto")).To(Succeed())
		_, out := getSettings(e, "memory")
		Expect(out.Settings[0].Value).To(Equal("ask"))
	})
})
