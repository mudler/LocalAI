package studio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("folder capacity", func() {
	It("starts create and send although more shared folders exist than fit", func() {
		ctx := context.Background()
		b, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), b, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(ws.Close()).To(Succeed()) })
		f := files.New(ws)
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		_, err = ws.Mkdir(ctx, f.Actor("alice"), session.HomePath("alice")+"/PrivateWork")
		Expect(err).NotTo(HaveOccurred())
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ags, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		harness := studiotest.NewGateHarness()
		svc, err := studiosvc.New(studiosvc.Deps{Store: st, Agents: ags, Files: f, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: harness})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		ag, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "test"}, &studiosvc.AgentFolderWrite{Path: "/PrivateWork", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		rec, err := svc.Create(ctx, "alice", studiosvc.CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		for i := 0; i < 31; i++ {
			n, err := ws.Mkdir(ctx, admin, fmt.Sprintf("/shared/SecretShare%02d", i))
			Expect(err).NotTo(HaveOccurred())
			Expect(ws.Grant(ctx, admin, files.EveryoneGroup, n.Path, workspace.ModeRead)).To(Succeed())
		}
		h := New(svc, nil)
		for _, send := range []bool{false, true} {
			body := fmt.Sprintf(`{"agent_id":%q}`, ag.ID)
			path := "/api/chats"
			if send {
				body = `{"text":"hi"}`
				path += "/" + rec.ID + "/messages"
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			out := httptest.NewRecorder()
			c := echo.New().NewContext(req, out)
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleAdmin})
			var err error
			if send {
				c.SetParamNames("id")
				c.SetParamValues(rec.ID)
				err = h.Send(c)
			} else {
				err = h.Create(c)
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Code).To(BeNumerically("<", 300), "send=%v: %s", send, out.Body.String())
		}
	})
})
