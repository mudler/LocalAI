package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The add-on list carries commands_host_exec, a read-only copy of the
// chats_host_exec mode, on the chat endpoint and on the draft endpoint.
var _ = Describe("commands_host_exec in the add-on list", func() {
	rigWith := func(mode func() string) (*studiosvc.Service, *echo.Echo) {
		h := &studiotest.ToolHarness{Script: func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "ok"}}
		}}
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studiosvc.New(studiosvc.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
			Harness: h, HostExecMode: mode})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		hs := New(svc, nil)
		e := echo.New()
		e.GET("/api/chats/:id/addons", hs.ListChatAddons)
		e.GET("/api/chats/draft/addons", hs.DraftAddons)
		return svc, e
	}
	get := func(e *echo.Echo, path string) schema.ChatsAddonList {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var out schema.ChatsAddonList
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		return out
	}
	DescribeTable("reports the mode on both endpoints",
		func(mode func() string, want string) {
			svc, e := rigWith(mode)
			rec, err := svc.Create(context.Background(), "", studiosvc.CreateInput{Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			Expect(get(e, "/api/chats/"+rec.ID+"/addons").CommandsHostExec).To(Equal(want))
			Expect(get(e, "/api/chats/draft/addons?model=m").CommandsHostExec).To(Equal(want))
		},
		Entry("off", func() string { return "off" }, "off"),
		Entry("ask", func() string { return "ask" }, "ask"),
		Entry("on", func() string { return "on" }, "on"),
		Entry("no setting reads as off", nil, "off"),
		Entry("an unknown value reads as off", func() string { return "maybe" }, "off"),
	)
})
