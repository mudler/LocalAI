// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// An administrator (root) shares a folder with alice through the API, fills it
// with a page, and alice finds the folder and its page, allows the page and
// runs it; taking the grant away makes both disappear. bob and dave are users
// with no grant; carol has no chats.
var _ = Describe("Chats shared folders endpoints", func() {
	var (
		s *studioServer
		f *files.Service
	)
	const admin = "/api/chats/admin/shared-folders"
	known := map[string]string{"alice": "alice", "bob": "bob", "carol": "carol", "dave": "dave", "root": "root", "alice@example.test": "alice"}

	BeforeEach(func() {
		f = newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			d.FindUser = func(_ context.Context, ref string) (*auth.User, error) {
				if id, ok := known[ref]; ok {
					return &auth.User{ID: id, Name: strings.ToUpper(id[:1]) + id[1:]}, nil
				}
				return nil, studio.ErrNoUser
			}
		})
	})
	create := func(name string) map[string]any {
		GinkgoHelper()
		rec := s.do("root", http.MethodPost, admin, map[string]any{"name": name})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)
	}
	idOf := func(folder map[string]any) string { return fmt.Sprint(int64(folder["id"].(float64))) }
	member := func(id, who, mode string) map[string]any {
		GinkgoHelper()
		rec := s.do("root", http.MethodPut, admin+"/"+id+"/members/"+who, map[string]any{"mode": mode})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)
	}

	It("needs an administrator for every admin route and a login for the user routes", func() {
		routes := []struct{ method, path string }{
			{http.MethodGet, admin}, {http.MethodPost, admin}, {http.MethodPost, admin + "/restore"}, {http.MethodGet, admin + "/events"},
			{http.MethodGet, admin + "/1"}, {http.MethodPatch, admin + "/1"}, {http.MethodDelete, admin + "/1"},
			{http.MethodPut, admin + "/1/members/alice"}, {http.MethodDelete, admin + "/1/members/alice"},
		}
		for _, r := range routes {
			Expect(s.do("", r.method, r.path, nil).Code).To(Equal(http.StatusUnauthorized), r.method+" "+r.path)
			Expect(s.do("alice", r.method, r.path, nil).Code).To(Equal(http.StatusForbidden), r.method+" "+r.path)
		}
		Expect(s.do("", http.MethodGet, "/api/chats/shared-folders", nil).Code).To(Equal(http.StatusUnauthorized))
		Expect(s.do("carol", http.MethodGet, "/api/chats/shared-folders", nil).Code).To(Equal(http.StatusForbidden), "carol has no chats")
	})

	It("shares a folder end to end: grant, page, consent, revoke", func() {
		team := create("Team")
		Expect(team).To(HaveKeyWithValue("path", "/shared/Team"))
		Expect(team["members"]).To(ConsistOf(And(HaveKeyWithValue("principal", "root"), HaveKeyWithValue("mode", "write"), HaveKeyWithValue("name", "Root"))))
		id := idOf(team)
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-folders", nil))["folders"]).To(BeEmpty(), "no grant yet")

		got := member(id, "alice@example.test", "read")
		Expect(got["members"]).To(ContainElement(And(HaveKeyWithValue("principal", "alice"), HaveKeyWithValue("mode", "read"))))

		// root edits the folder with the grant the creation gave.
		_, err := f.Store().Put(context.Background(), f.Actor("root"), "/shared/Team/pages/standup/index.html", strings.NewReader("<p>standup</p>\n"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())

		mine := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-folders", nil))["folders"].([]any)
		Expect(mine).To(ConsistOf(And(HaveKeyWithValue("name", "Team"), HaveKeyWithValue("mode", "read"), HaveKeyWithValue("pages", BeNumerically("==", 1)))))
		Expect(mine[0]).NotTo(HaveKey("members"), "a member does not see the other members")
		Expect(s.do("alice", http.MethodGet, "/api/chats/shared-folders/"+id, nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("bob", http.MethodGet, "/api/chats/shared-folders/"+id, nil).Code).To(Equal(http.StatusNotFound), "a folder that bob has no grant for")
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/shared-folders", nil))["folders"]).To(BeEmpty())

		pages := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-pages", nil))["pages"].([]any)
		Expect(pages).To(HaveLen(1))
		page := pages[0].(map[string]any)
		Expect(page).To(HaveKeyWithValue("consent", "none"))
		Expect(fmt.Sprint(int64(page["folder_id"].(float64)))).To(Equal(id), "a shared page names its folder by the folder's id")
		base := "/api/chats/shared-pages/" + id + "/standup"
		rec := s.do("alice", http.MethodPut, base+"/consent", map[string]any{"hash": page["hash"]})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(s.do("alice", http.MethodGet, base, nil))["consent"]).To(HaveKeyWithValue("state", "allowed"))

		rec = s.do("root", http.MethodDelete, admin+"/"+id+"/members/alice", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-pages", nil))["pages"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodGet, base, nil).Code).To(Equal(http.StatusNotFound))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-folders", nil))["folders"]).To(BeEmpty())

		evs := decodeJSON(s.do("root", http.MethodGet, admin+"/events", nil))["events"].([]any)
		actions := []string{}
		for _, e := range evs {
			m := e.(map[string]any)
			Expect(m).To(HaveKeyWithValue("actor", "root"))
			actions = append(actions, fmt.Sprint(m["action"], " ", m["principal"]))
		}
		Expect(actions).To(Equal([]string{"revoke alice", "grant alice", "grant root", "create <nil>"}))
	})

	It("shares with everyone, renames, removes with an undo and validates", func() {
		team := create("Team")
		id := idOf(team)
		// A client escapes the "@" of the member in the path.
		got := member(id, "%40everyone", "write")
		Expect(got["members"].([]any)[0]).To(And(HaveKeyWithValue("principal", "@everyone"), HaveKeyWithValue("everyone", true)))
		Expect(decodeJSON(s.do("dave", http.MethodGet, "/api/chats/shared-folders", nil))["folders"]).To(ConsistOf(HaveKeyWithValue("mode", "write")))

		Expect(s.do("root", http.MethodPost, admin, map[string]any{"name": "team"}).Code).To(Equal(http.StatusConflict))
		rec := s.do("root", http.MethodPost, admin, map[string]any{"name": "../up"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("a folder name is"))
		rec = s.do("root", http.MethodPut, admin+"/"+id+"/members/nobody", map[string]any{"mode": "read"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("no such user"))
		Expect(s.do("root", http.MethodPut, admin+"/"+id+"/members/alice", map[string]any{"mode": "owner"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("root", http.MethodGet, admin+"/999999", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("root", http.MethodGet, admin+"/x", nil).Code).To(Equal(http.StatusNotFound))

		rec = s.do("root", http.MethodPatch, admin+"/"+id, map[string]any{"name": "Crew"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("path", "/shared/Crew"))

		rec = s.do("root", http.MethodDelete, admin+"/"+id, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		rev := decodeJSON(rec)["undo_rev"]
		Expect(rev).To(BeNumerically(">", 0))
		Expect(decodeJSON(s.do("root", http.MethodGet, admin, nil))["folders"]).To(BeEmpty())
		Expect(decodeJSON(s.do("dave", http.MethodGet, "/api/chats/shared-folders", nil))["folders"]).To(BeEmpty())

		Expect(s.do("root", http.MethodPost, admin+"/restore", map[string]any{"rev": 1}).Code).To(Equal(http.StatusBadRequest))
		rec = s.do("root", http.MethodPost, admin+"/restore", map[string]any{"rev": rev})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("name", "Crew"))
		Expect(decodeJSON(s.do("dave", http.MethodGet, "/api/chats/shared-folders", nil))["folders"]).To(HaveLen(1), "the grant came back")
	})
})
