// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

type statusCollections struct{ names []string }

func (*statusCollections) Allowed(context.Context, string) bool             { return true }
func (b *statusCollections) List(context.Context, string) ([]string, error) { return b.names, nil }
func (*statusCollections) Search(context.Context, string, string, string, int) ([]studiosvc.CollectionHit, error) {
	return nil, nil
}

var _ = Describe("memory REST contract", func() {
	It("enforces privacy, ownership and live retry authorization", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag := agents.Agent{ID: "agent", Principal: "alice", Name: "writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "docs", ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		put := func() {
			Expect(as.Put(ctx, ag)).To(Succeed())
		}
		put()
		for _, id := range []string{"chat", "other"} {
			Expect(st.CreateChat(ctx, session.ChatRecord{ID: id, Principal: "alice", AgentID: "agent"})).To(Succeed())
		}
		_, err = st.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "secret-source", Text: "secret-body", Policy: store.CapturePolicy{AgentID: "agent", Destination: "docs", Mode: store.CaptureIncoming}})
		Expect(err).NotTo(HaveOccurred())
		_, err = st.ClaimCapture(ctx, "worker", time.Now().Add(25*time.Hour), time.Minute)
		Expect(err).NotTo(HaveOccurred())
		records, err := st.ListCaptures(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		id := records[0].ID
		backend := &statusCollections{names: []string{"docs"}}
		svc, err := studiosvc.New(studiosvc.Deps{Store: st, Agents: as, Collections: backend, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		h := New(svc, nil)
		call := func(retry bool, user, chat, capture string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			out := httptest.NewRecorder()
			c := echo.New().NewContext(req, out)
			c.Set("auth_user", &auth.User{ID: user})
			c.SetParamNames("id", "capture_id")
			c.SetParamValues(chat, capture)
			var err error
			if retry {
				err = h.RetryMemoryCapture(c)
			} else {
				err = h.MemoryStatuses(c)
			}
			Expect(err).NotTo(HaveOccurred())
			return out
		}
		expect := func(out *httptest.ResponseRecorder, code int) {
			Expect(out.Code).To(Equal(code), out.Body.String())
		}
		expect(call(false, "bob", "chat", id), 404)
		expect(call(true, "bob", "chat", id), 404)
		expect(call(true, "alice", "other", id), 404)
		out := call(false, "alice", "chat", id)
		expect(out, 200)
		for _, secret := range []string{"secret-body", "secret-source", "alice", "StateJSON", "BackendFence"} {
			Expect(out.Body.String()).NotTo(ContainSubstring(secret))
		}
		var statuses []schema.ChatsMemoryCapture
		Expect(json.Unmarshal(out.Body.Bytes(), &statuses)).To(Succeed())
		Expect(statuses).To(HaveLen(1))
		Expect(statuses[0].Status).To(Equal("failed"))
		Expect(statuses[0].RetryEligible).To(BeTrue())
		for _, deny := range []string{"off", "destination", "revoked", "deleted"} {
			ag.Memory.AutomaticIngestion = "incoming_only"
			ag.Memory.Destination = "docs"
			backend.names = []string{"docs"}
			switch deny {
			case "off":
				ag.Memory.AutomaticIngestion = "off"
			case "destination":
				ag.Memory.Destination = "other"
				ag.CollectionPermissions.Grants = append(ag.CollectionPermissions.Grants, agents.CollectionGrant{Collection: "other"})
			case "revoked":
				backend.names = nil
			}
			put()
			if deny == "deleted" {
				Expect(as.Delete(ctx, "alice", "agent")).To(Succeed())
			}
			expect(call(true, "alice", "chat", id), 403)
			expect(call(false, "alice", "chat", id), 200)
		}
		ag.Memory.AutomaticIngestion = "incoming_only"
		ag.Memory.Destination = "docs"
		backend.names = []string{"docs"}
		put()
		out = call(true, "alice", "chat", id)
		expect(out, 200)
		var retried schema.ChatsMemoryCapture
		Expect(json.Unmarshal(out.Body.Bytes(), &retried)).To(Succeed())
		Expect(retried.Status).To(Equal("queued"))
		Expect(retried.ID).To(Equal(id))
		expect(call(true, "alice", "chat", id), 409)
		Expect(st.RecordMemoryAcceptanceFailure(ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "unavailable", Text: "unretained-secret", Policy: store.CapturePolicy{AgentID: "agent", Destination: "docs", Mode: store.CaptureIncoming}})).To(Succeed())
		records, err = st.ListCaptures(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		for _, r := range records {
			if r.CaptureUnavailable {
				expect(call(true, "alice", "chat", r.ID), 409)
				out = call(false, "alice", "chat", r.ID)
				expect(out, 200)
				Expect(json.Unmarshal(out.Body.Bytes(), &statuses)).To(Succeed())
				for _, v := range statuses {
					if v.ID == r.ID {
						Expect(v.RetryEligible).To(BeFalse(), "metadata-only capture marked retry eligible")
					}
				}
			}
		}

	})
})
