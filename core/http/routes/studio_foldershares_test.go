// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// accounts is the account directory of the test server's users. Like the
// picker of the sharing routes it takes three characters or more and refuses
// a search of "slow", as over the rate.
type accounts map[string]string

func (a accounts) Accounts(_ context.Context, _, q string, limit int) ([]sharedfolders.User, error) {
	switch {
	case len([]rune(q)) < 3:
		return nil, sharedfolders.ErrBadQuery
	case q == "slow":
		return nil, sharedfolders.ErrSearchLimited
	}
	out := []sharedfolders.User{}
	for _, id := range []string{"alice", "bob", "carol", "dave", "root"} {
		if strings.HasPrefix(strings.ToLower(a[id]), strings.ToLower(q)) && len(out) < limit {
			out = append(out, sharedfolders.User{ID: id, Name: a[id]})
		}
	}
	return out, nil
}

func (a accounts) Active(_ context.Context, id string) (sharedfolders.User, error) {
	if name, ok := a[id]; ok {
		return sharedfolders.User{ID: id, Name: name}, nil
	}
	return sharedfolders.User{}, sharedfolders.ErrUnknownUser
}

var people = accounts{"alice": "Alice", "bob": "Bob", "carol": "Carol", "dave": "Dave", "root": "Root"}

// alice shares a folder of her home with bob through the routes a browser
// tab uses; nothing but her own browser session may read or change who has it.
var _ = Describe("Chats folder sharing endpoints", func() {
	const base = "/api/chats/files/shares"
	var (
		s *studioServer
		f *files.Service
	)
	BeforeEach(func() {
		f = newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) { d.FolderDirectory = people })
		for _, u := range []string{"alice", "bob", "dave", "root"} {
			Expect(f.EnsureHome(context.Background(), u)).To(Succeed())
		}
	})
	// human sends what a same-origin fetch of the Files page sends.
	human := func(user, method, path, confirm string, body any) *httptest.ResponseRecorder {
		req := s.request(user, method, path, body)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("X-LocalAI-Confirm", confirm)
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	mkdir := func(user, p string) {
		GinkgoHelper()
		_, err := f.Store().Mkdir(context.Background(), f.Actor(user), p)
		Expect(err).NotTo(HaveOccurred())
	}
	find := func(user, p string) string {
		GinkgoHelper()
		rec := s.do(user, http.MethodPost, base, map[string]any{"path": p})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return fmt.Sprint(int64(decodeJSON(rec)["id"].(float64)))
	}

	It("shares a folder of the owner's home read and write, and the grantee finds it with the owner's name", func() {
		mkdir("alice", "/users/alice/Projects")
		id := find("alice", "/Projects")
		rec := human("alice", http.MethodPut, base+"/"+id+"/grants/bob", id, map[string]any{"role": "read"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["grants"]).To(ConsistOf(map[string]any{"userId": "bob", "displayName": "Bob", "role": "read"}))

		mine := decodeJSON(s.do("bob", http.MethodGet, "/api/chats/shared-folders", nil))["folders"].([]any)
		Expect(mine).To(ConsistOf(And(HaveKeyWithValue("name", "Projects"), HaveKeyWithValue("owner", "Alice"),
			HaveKeyWithValue("mode", "read"), HaveKeyWithValue("path", ""))))
		_, err := f.Store().Put(context.Background(), f.Actor("bob"), "/users/alice/Projects/x.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrReadOnly))

		Expect(human("alice", http.MethodPut, base+"/"+id+"/grants/bob", id, map[string]any{"role": "write"}).Code).To(Equal(http.StatusOK))
		_, err = f.Store().Put(context.Background(), f.Actor("bob"), "/users/alice/Projects/x.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		rec = human("alice", http.MethodPut, base+"/"+id+"/grants/@everyone", id, map[string]any{"role": "read"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["grants"]).To(ContainElement(map[string]any{"userId": "@everyone", "displayName": "", "role": "read"}))

		list := decodeJSON(s.do("alice", http.MethodGet, base, nil))
		Expect(list["sharing"]).To(BeTrue())
		Expect(list["folders"]).To(ConsistOf(And(HaveKeyWithValue("path", "/Projects"), HaveKeyWithValue("sharedWith", BeEquivalentTo(2)), HaveKeyWithValue("everyone", true))))

		rec = human("alice", http.MethodDelete, base+"/"+id+"/grants/bob", id, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		_, err = f.Store().Stat(context.Background(), f.Actor("bob"), "/users/alice/Projects/x.txt")
		Expect(err).NotTo(HaveOccurred(), "bob keeps what everyone has")
		Expect(human("alice", http.MethodDelete, base+"/"+id+"/grants/@everyone", id, nil).Code).To(Equal(http.StatusOK))
		_, err = f.Store().Stat(context.Background(), f.Actor("bob"), "/users/alice/Projects/x.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "the revoke holds at bob's next call")
	})

	It("accepts a change, the grants and the picker only from the owner's own browser session", func() {
		mkdir("alice", "/users/alice/Projects")
		id := find("alice", "/Projects")
		calls := []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, base + "/" + id + "/grants", nil},
			{http.MethodPut, base + "/" + id + "/grants/bob", map[string]any{"role": "read"}},
			{http.MethodDelete, base + "/" + id + "/grants/bob", nil},
			{http.MethodGet, base + "/" + id + "/users?q=bob", nil},
		}
		for _, c := range calls {
			name := c.method + " " + c.path
			Expect(s.do("alice", c.method, c.path, c.body).Code).To(Equal(http.StatusForbidden), name+": a session without the browser headers")
			Expect(human("alice", c.method, c.path, "999", c.body).Code).To(Equal(http.StatusForbidden), name+": the confirmation names another folder")
			for _, marker := range []string{"X-LocalAI-Tool-Call", "X-LocalAI-Agent", "Mcp-Session-Id"} {
				req := s.request("alice", c.method, c.path, c.body)
				for k, v := range map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "X-LocalAI-Confirm": id, marker: "1"} {
					req.Header.Set(k, v)
				}
				rec := httptest.NewRecorder()
				s.e.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusForbidden), name+": "+marker)
			}
			Expect(human("", c.method, c.path, id, c.body).Code).To(Equal(http.StatusUnauthorized), name)
		}
		short := human("alice", http.MethodGet, base+"/"+id+"/users?q=bo", id, nil)
		Expect(short.Code).To(Equal(http.StatusBadRequest), "two characters are too short")
		Expect(decodeJSON(short)).To(HaveKey("error"))
		users := decodeJSON(human("alice", http.MethodGet, base+"/"+id+"/users?q=ali", id, nil))
		Expect(users["users"]).To(BeEmpty(), "never the owner")
		users = decodeJSON(human("alice", http.MethodGet, base+"/"+id+"/users?q=bob", id, nil))
		Expect(users["users"]).To(ConsistOf(map[string]any{"id": "bob", "displayName": "Bob"}))
		limited := human("alice", http.MethodGet, base+"/"+id+"/users?q=slow", id, nil)
		Expect(limited.Code).To(Equal(http.StatusTooManyRequests))
		Expect(limited.Body.String()).To(ContainSubstring("rate_limited"))
	})

	It("gives an unknown folder, another person's folder, an administrator's folder and a managed folder the same answer", func() {
		mkdir("bob", "/users/bob/Hers")
		bobs := find("bob", "/Hers")
		rec := s.do("root", http.MethodPost, "/api/chats/admin/shared-folders", map[string]any{"name": "Team"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		team := fmt.Sprint(int64(decodeJSON(rec)["id"].(float64)))
		Expect(s.do("root", http.MethodPut, "/api/chats/admin/shared-folders/"+team+"/members/alice", map[string]any{"mode": "write"}).Code).To(Equal(http.StatusOK))
		Expect(human("bob", http.MethodPut, base+"/"+bobs+"/grants/alice", bobs, map[string]any{"role": "read"}).Code).To(Equal(http.StatusOK))

		var bodies []string
		for _, id := range []string{bobs, team, "424242"} {
			rec := human("alice", http.MethodPut, base+"/"+id+"/grants/dave", id, map[string]any{"role": "read"})
			Expect(rec.Code).To(Equal(http.StatusForbidden), id)
			bodies = append(bodies, rec.Body.String())
		}
		for _, p := range []string{"/", "/LocalAI", "/Chats", "/missing", "/../bob/Hers"} {
			rec := s.do("alice", http.MethodPost, base, map[string]any{"path": p})
			Expect(rec.Code).To(Equal(http.StatusForbidden), p)
			bodies = append(bodies, rec.Body.String())
		}
		for _, b := range bodies {
			Expect(b).To(Equal(bodies[0]), "every refusal reads the same")
		}
		admin := decodeJSON(s.do("root", http.MethodGet, "/api/chats/admin/shared-folders/"+team, nil))
		Expect(admin["members"]).To(HaveLen(2), "the administrator's folder is unchanged")
	})

	It("refuses the folders LocalAI reads as the owner's own, and a move that takes a share into one", func() {
		mkdir("alice", "/users/alice/Projects")
		for _, p := range []string{"/.skills", "/Memory", "/pages", "/Projects/.skills"} {
			mkdir("alice", "/users/alice"+p)
			rec := s.do("alice", http.MethodPost, base, map[string]any{"path": p})
			Expect(rec.Code).To(Equal(http.StatusForbidden), p)
		}
		id := find("alice", "/Projects")
		Expect(human("alice", http.MethodPut, base+"/"+id+"/grants/bob", id, map[string]any{"role": "write"}).Code).To(Equal(http.StatusOK))

		rec := s.do("alice", http.MethodPost, "/api/chats/files/move", map[string]string{"from": "/Projects", "to": "/LocalAI/Projects"})
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("folder_shared"))
		rec = s.do("alice", http.MethodPost, "/api/chats/files/move", map[string]string{"from": "/Projects", "to": "/Archive/Projects"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	})

	It("says sharing is off on a server without accounts, and lists nothing", func() {
		off := newStudioServerWith(newTestFiles(), nil)
		list := decodeJSON(off.do("alice", http.MethodGet, base, nil))
		Expect(list["sharing"]).To(BeFalse())
		Expect(list["folders"]).To(BeEmpty())
		rec := off.do("alice", http.MethodPost, base, map[string]any{"path": "/Projects"})
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring("sharing_disabled"))
	})
})
