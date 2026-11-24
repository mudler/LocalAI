package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("importing a project agent with authentication off", func() {
	It("makes the project and saves the agent under the same (anonymous) owner", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		b, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), b, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(ws.Close()).To(Succeed()) })
		f := files.New(ws)
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studiosvc.New(studiosvc.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
			Harness: studiotest.NewGateHarness(), Agents: ag, Files: f, Projects: projects.NewService(projects.Options{Store: ps, Files: f})})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		h := New(svc, nil)
		e := echo.New()
		e.POST("/api/chats/agents/import", h.ImportAgent)

		body := `{"version":5,"kind":"localai-agent","agent":{"name":"Planner","projectRef":{"name":"Acme"},"folders":[{"path":"files","mode":"write"},{"path":"memory","mode":"read"}]}}`
		req := httptest.NewRequest(http.MethodPost, "/api/chats/agents/import?create_project=true", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req) // no user: authentication is off
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())

		list, err := svc.Agents(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ProjectID).NotTo(BeEmpty())
		rows, err := svc.ProjectAgents(ctx, "", list[0].ProjectID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Unavailable).To(BeFalse())
	})
})
