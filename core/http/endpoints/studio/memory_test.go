// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestMemoryRESTContract(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	as, err := agents.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	ag := agents.Agent{ID: "agent", Principal: "alice", Name: "writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "docs", ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
	put := func() {
		t.Helper()
		if err := as.Put(ctx, ag); err != nil {
			t.Fatal(err)
		}
	}
	put()
	for _, id := range []string{"chat", "other"} {
		if err := st.CreateChat(ctx, session.ChatRecord{ID: id, Principal: "alice", AgentID: "agent"}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = st.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "secret-source", Text: "secret-body", Policy: store.CapturePolicy{AgentID: "agent", Destination: "docs", Mode: store.CaptureIncoming}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimCapture(ctx, "worker", time.Now().Add(25*time.Hour), time.Minute); err != nil {
		t.Fatal(err)
	}
	records, err := st.ListCaptures(ctx, "alice", "chat")
	if err != nil {
		t.Fatal(err)
	}
	id := records[0].ID
	backend := &statusCollections{names: []string{"docs"}}
	svc, err := studiosvc.New(studiosvc.Deps{Store: st, Agents: as, Collections: backend, Provider: provider.New(), StateDir: t.TempDir(), Harness: studiotest.NewGateHarness()})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close(ctx)
	h := New(svc, nil)
	call := func(retry bool, user, chat, capture string) *httptest.ResponseRecorder {
		t.Helper()
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
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	expect := func(out *httptest.ResponseRecorder, code int) {
		t.Helper()
		if out.Code != code {
			t.Fatalf("want %d got %d: %s", code, out.Code, out.Body.String())
		}
	}
	expect(call(false, "bob", "chat", id), 404)
	expect(call(true, "bob", "chat", id), 404)
	expect(call(true, "alice", "other", id), 404)
	out := call(false, "alice", "chat", id)
	expect(out, 200)
	for _, secret := range []string{"secret-body", "secret-source", "alice", "StateJSON", "BackendFence"} {
		if strings.Contains(out.Body.String(), secret) {
			t.Fatalf("leak %s", out.Body.String())
		}
	}
	var statuses []schema.ChatsMemoryCapture
	if err := json.Unmarshal(out.Body.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Status != "failed" || !statuses[0].RetryEligible {
		t.Fatalf("unexpected %s", out.Body.String())
	}
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
			if err := as.Delete(ctx, "alice", "agent"); err != nil {
				t.Fatal(err)
			}
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
	if err := json.Unmarshal(out.Body.Bytes(), &retried); err != nil {
		t.Fatal(err)
	}
	if retried.Status != "queued" || retried.ID != id {
		t.Fatalf("unexpected retry %s", out.Body.String())
	}
	expect(call(true, "alice", "chat", id), 409)
	if err := st.RecordMemoryAcceptanceFailure(ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "unavailable", Text: "unretained-secret", Policy: store.CapturePolicy{AgentID: "agent", Destination: "docs", Mode: store.CaptureIncoming}}); err != nil {
		t.Fatal(err)
	}
	records, err = st.ListCaptures(ctx, "alice", "chat")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.CaptureUnavailable {
			expect(call(true, "alice", "chat", r.ID), 409)
			out = call(false, "alice", "chat", r.ID)
			expect(out, 200)
			if err := json.Unmarshal(out.Body.Bytes(), &statuses); err != nil {
				t.Fatal(err)
			}
			for _, v := range statuses {
				if v.ID == r.ID && v.RetryEligible {
					t.Fatal("metadata-only capture marked retry eligible")
				}
			}
		}
	}

}
