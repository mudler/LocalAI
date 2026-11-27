package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

var _ = Describe("the plugins of a saved agent", func() {
	It("are saved, shown, exported and imported as an exact grant list", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studiosvc.New(studiosvc.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
			Harness: studiotest.NewGateHarness(), Agents: ag})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		h := New(svc, nil)
		e := echo.New()
		e.POST("/api/chats/agents", h.CreateAgent)
		e.GET("/api/chats/agents/:id/export", h.ExportAgent)
		e.POST("/api/chats/agents/import", h.ImportAgent)
		do := func(method, path, body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		rec := do(http.MethodPost, "/api/chats/agents", `{"name":"Fmt","plugins":["in-1","in-2"]}`)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		var out schema.ChatsAgent
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Plugins).To(Equal([]string{"in-1", "in-2"}))

		rec = do(http.MethodGet, "/api/chats/agents/"+out.ID+"/export", "")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var file schema.ChatsAgentExport
		Expect(json.Unmarshal(rec.Body.Bytes(), &file)).To(Succeed())
		Expect(file.Agent.Plugins).To(Equal([]string{"in-1", "in-2"}))

		file.Agent.Name = "Fmt copy"
		body, err := json.Marshal(file)
		Expect(err).NotTo(HaveOccurred())
		rec = do(http.MethodPost, "/api/chats/agents/import", string(body))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		list, err := svc.Agents(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		var imported agents.Agent
		for _, a := range list {
			if a.Name == "Fmt copy" {
				imported = a
			}
		}
		Expect(imported.Plugins).To(Equal([]string{"in-1", "in-2"}))

		rec = do(http.MethodPost, "/api/chats/agents", `{"name":"Bad","plugins":[""]}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
	})
})
