// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

var _ = Describe("the secret of an event trigger in the studio API", func() {
	ctx := context.Background()
	var s *studioServer

	BeforeEach(func() { s = newStudioServer() })

	eventBody := func(mod func(map[string]any)) map[string]any {
		b := map[string]any{"name": "Pull requests", "prompt": "Review the pull request.", "kind": "event", "source": "example-hook",
			"event_type": "push", "matches": []string{"ref=main"}, "model": "test-model", "enabled": true}
		if mod != nil {
			mod(b)
		}
		return b
	}
	create := func(user string, body map[string]any) (string, string) {
		rec := s.do(user, http.MethodPost, "/api/chats/triggers", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		out := decodeJSON(rec)
		secret, _ := out["secret"].(string)
		return out["id"].(string), secret
	}
	hook := func(id, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/chats/triggers/"+id+"/hook?source=example-hook", strings.NewReader(`{"type":"push","ref":"main"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}

	It("creates an event trigger and returns a random secret once", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/triggers", eventBody(nil))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		out := decodeJSON(rec)
		id, secret := out["id"].(string), out["secret"].(string)
		raw, err := base64.RawURLEncoding.DecodeString(secret)
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(HaveLen(32))
		Expect(out["kind"]).To(Equal("event"))
		Expect(out["source"]).To(Equal("example-hook"))
		Expect(out["event_type"]).To(Equal("push"))
		Expect(out["matches"]).To(ConsistOf("ref=main"))
		Expect(out["hook_path"]).To(Equal("/api/chats/triggers/" + id + "/hook"))
		Expect(out["enabled"]).To(BeTrue())
		other, secret2 := create("alice", eventBody(func(b map[string]any) { b["name"] = "Another" }))
		Expect(other).NotTo(Equal(id))
		Expect(secret2).NotTo(Equal(secret))
		Expect(hook(id, secret).Code).To(Equal(http.StatusAccepted))
	})

	It("has no secret on a trigger of another kind, and refuses event fields on it", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{"name": "Daily", "prompt": "p", "kind": "schedule",
			"schedule": "daily 09:00", "time_zone": "UTC", "model": "test-model", "enabled": true})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)).NotTo(HaveKey("secret"))
		Expect(decodeJSON(rec)).NotTo(HaveKey("hook_path"))
		bad := s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{"name": "Daily 2", "prompt": "p", "kind": "schedule",
			"schedule": "daily 09:00", "time_zone": "UTC", "model": "test-model", "source": "example-hook"})
		Expect(bad.Code).To(Equal(http.StatusBadRequest), bad.Body.String())
	})

	It("checks an event trigger as it checks the other kinds", func() {
		for name, mod := range map[string]func(map[string]any){
			"no source":    func(b map[string]any) { delete(b, "source") },
			"bad match":    func(b map[string]any) { b["matches"] = []string{"nofield"} },
			"no model":     func(b map[string]any) { delete(b, "model") },
			"empty prompt": func(b map[string]any) { b["prompt"] = "" },
		} {
			rec := s.do("alice", http.MethodPost, "/api/chats/triggers", eventBody(mod))
			Expect(rec.Code).To(Equal(http.StatusBadRequest), name+": "+rec.Body.String())
		}
	})

	It("keeps the secret out of every view of the trigger", func() {
		id, secret := create("alice", eventBody(nil))
		Expect(hook(id, secret).Code).To(Equal(http.StatusAccepted))
		// A trigger that waits as a proposal, with a secret in the store.
		Expect(s.trigStore.PutTrigger(ctx, triggers.Trigger{ID: "prop1", Owner: "alice", Name: "Proposed", Prompt: "p", Kind: triggers.KindEvent,
			Source: "example-hook", Model: "test-model", Proposed: true, Secret: "secret-of-a-proposal"})).To(Succeed())

		views := map[string]*httptest.ResponseRecorder{
			"get":        s.do("alice", http.MethodGet, "/api/chats/triggers/"+id, nil),
			"list":       s.do("alice", http.MethodGet, "/api/chats/triggers", nil),
			"runs":       s.do("alice", http.MethodGet, "/api/chats/triggers/"+id+"/runs", nil),
			"admin list": s.do("root", http.MethodGet, "/api/chats/admin/triggers", nil),
			"admin runs": s.do("root", http.MethodGet, "/api/chats/admin/triggers/"+id+"/runs", nil),
			"proposals":  s.do("alice", http.MethodGet, "/api/chats/triggers/proposals", nil),
			"proposal":   s.do("alice", http.MethodGet, "/api/chats/triggers/proposals/prop1", nil),
			"update":     s.do("alice", http.MethodPut, "/api/chats/triggers/"+id, eventBody(func(b map[string]any) { b["name"] = "Renamed" })),
			"disable":    s.do("alice", http.MethodPost, "/api/chats/triggers/"+id+"/disable", nil),
			"enable":     s.do("alice", http.MethodPost, "/api/chats/triggers/"+id+"/enable", nil),
			"change":     s.do("alice", http.MethodGet, "/api/chats/triggers/changes/none", nil),
		}
		for name, rec := range views {
			Expect(rec.Body.String()).NotTo(ContainSubstring(secret), name)
			Expect(rec.Body.String()).NotTo(ContainSubstring("secret-of-a-proposal"), name)
			Expect(strings.ToLower(rec.Body.String())).NotTo(ContainSubstring(`"secret"`), name)
		}
		for _, name := range []string{"get", "list", "update", "enable", "admin list"} {
			Expect(views[name].Code).To(Equal(http.StatusOK), name+": "+views[name].Body.String())
		}
		Expect(views["get"].Body.String()).To(ContainSubstring(`"hook_path"`))
	})

	It("keeps the stored secret on an update and ignores one the client sends", func() {
		id, secret := create("alice", eventBody(nil))
		rec := s.do("alice", http.MethodPut, "/api/chats/triggers/"+id, eventBody(func(b map[string]any) {
			b["secret"] = "chosen-by-the-client"
			b["matches"] = []string{"ref=dev"}
		}))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("chosen-by-the-client"))
		stored, err := s.trigStore.GetTrigger(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Secret).To(Equal(secret))
		Expect(stored.Matches).To(ConsistOf("ref=dev"))
		Expect(hook(id, secret).Code).To(Equal(http.StatusAccepted))
		Expect(hook(id, "chosen-by-the-client").Code).To(Equal(http.StatusNotFound))
	})

	Describe("rotate", func() {
		It("returns a new secret, and the old one stops working at once", func() {
			id, old := create("alice", eventBody(nil))
			Expect(hook(id, old).Code).To(Equal(http.StatusAccepted))
			rec := s.do("alice", http.MethodPost, "/api/chats/triggers/"+id+"/secret", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			fresh := decodeJSON(rec)["secret"].(string)
			Expect(fresh).NotTo(BeEmpty())
			Expect(fresh).NotTo(Equal(old))
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
			refused := hook(id, old)
			Expect(refused.Code).To(Equal(http.StatusNotFound))
			Expect(strings.TrimSpace(refused.Body.String())).To(Equal(`{"error":"not found"}`))
			Expect(hook(id, fresh).Code).To(Equal(http.StatusAccepted))
			get := s.do("alice", http.MethodGet, "/api/chats/triggers/"+id, nil)
			Expect(get.Body.String()).NotTo(ContainSubstring(fresh))
		})

		It("is for the owner only", func() {
			id, secret := create("alice", eventBody(nil))
			for _, user := range []string{"bob", "root"} {
				rec := s.do(user, http.MethodPost, "/api/chats/triggers/"+id+"/secret", nil)
				Expect(rec.Code).To(Equal(http.StatusNotFound), user+": "+rec.Body.String())
				Expect(rec.Body.String()).NotTo(ContainSubstring(secret))
			}
			Expect(s.do("", http.MethodPost, "/api/chats/triggers/"+id+"/secret", nil).Code).To(Equal(http.StatusUnauthorized))
			Expect(hook(id, secret).Code).To(Equal(http.StatusAccepted), "the secret did not change")
		})

		It("is a 400 for a trigger that is not an event trigger, a 404 for a missing one", func() {
			rec := s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{"name": "Daily", "prompt": "p", "kind": "schedule",
				"schedule": "daily 09:00", "time_zone": "UTC", "model": "test-model", "enabled": true})
			id := decodeJSON(rec)["id"].(string)
			bad := s.do("alice", http.MethodPost, "/api/chats/triggers/"+id+"/secret", nil)
			Expect(bad.Code).To(Equal(http.StatusBadRequest), bad.Body.String())
			Expect(bad.Body.String()).To(ContainSubstring("only an event trigger has a secret"))
			Expect(s.do("alice", http.MethodPost, "/api/chats/triggers/missing/secret", nil).Code).To(Equal(http.StatusNotFound))
		})
	})
})
