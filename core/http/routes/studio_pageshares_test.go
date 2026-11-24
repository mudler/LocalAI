// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// alice owns the page "todo" and shares it with bob and root through the page
// routes, which only her own browser session may use. A grantee runs the page
// as themselves after they allow the version, and what they save goes into
// the page's folder in alice's home.
var _ = Describe("Chats page sharing endpoints", func() {
	const (
		shares = "/api/chats/pages/todo/shares"
		search = "/api/chats/pages/shares/search"
	)
	var (
		s       *studioServer
		f       *files.Service
		dir     accounts
		sharing atomic.Bool
		ctx     = context.Background()
	)
	put := func(user, p, body string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor(user), p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
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
	share := func(principal, role string) *httptest.ResponseRecorder {
		return human("alice", http.MethodPut, shares, "todo", map[string]any{"principal": principal, "role": role})
	}
	unshare := func(principal string) *httptest.ResponseRecorder {
		return human("alice", http.MethodDelete, shares+"/"+principal, "todo", nil)
	}
	grants := func() map[string]any {
		GinkgoHelper()
		rec := human("alice", http.MethodGet, shares, "todo", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)
	}
	bridge := func(user, method, path, hash string, body any) *httptest.ResponseRecorder {
		req := s.request(user, method, path, body)
		req.Header.Set("X-LocalAI-Page-Hash", hash)
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	// theirs is the page of user as the shared pages list has it.
	theirs := func(user string) map[string]any {
		GinkgoHelper()
		list := decodeJSON(s.do(user, http.MethodGet, "/api/chats/shared-pages", nil))["pages"].([]any)
		for _, p := range list {
			if m := p.(map[string]any); m["name"] == "todo" {
				return m
			}
		}
		return nil
	}
	pageBase := func(user string) string {
		GinkgoHelper()
		p := theirs(user)
		Expect(p).NotTo(BeNil(), "%s has the page", user)
		return fmt.Sprintf("/api/chats/shared-pages/%d/todo", int64(p["folder_id"].(float64)))
	}
	hashOf := func(user string) string {
		GinkgoHelper()
		return theirs(user)["hash"].(string)
	}
	allow := func(user string) string {
		GinkgoHelper()
		hash := hashOf(user)
		rec := s.do(user, http.MethodPut, pageBase(user)+"/consent", map[string]any{"hash": hash})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return hash
	}
	write := func(user, hash, rel string) *httptest.ResponseRecorder {
		return bridge(user, http.MethodPut, pageBase(user)+"/file", hash, map[string]any{"path": rel, "content": "{}"})
	}
	exists := func(p string) bool {
		_, err := f.Store().Stat(ctx, f.Actor("alice"), p)
		return err == nil
	}

	BeforeEach(func() {
		f = newTestFiles()
		dir = accounts{"alice": "Alice", "bob": "Bob", "carol": "Carol", "dave": "Dave", "root": "Root"}
		sharing.Store(true)
		s = newStudioServerWith(f, func(d *studio.Deps) {
			d.FolderDirectory = dir
			d.UserPageSharesOn = sharing.Load
		})
		for _, u := range []string{"alice", "bob", "root"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		put("alice", "/users/alice/pages/todo/index.html", "<p>todo</p>\n")
		put("alice", "/users/alice/pages/todo/page.json", `{"title":"Todo"}`)
		put("alice", "/users/alice/pages/todo/data/items.json", "[]")
		put("bob", "/users/bob/pages/theirs/index.html", "<p>bob</p>\n")
		put("alice", "/users/alice/Site/pages/home/index.html", "<p>home</p>\n")
	})

	It("shares an own page and nothing else", func() {
		// Before the page is shared, the folder routes refuse it, pages/ and a
		// folder in it.
		for _, p := range []string{"/pages", "/pages/todo", "/pages/todo/data"} {
			Expect(s.do("alice", http.MethodPost, "/api/chats/files/shares", map[string]any{"path": p}).Code).To(Equal(http.StatusForbidden), p)
		}
		rec := share("bob", "use")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		got := decodeJSON(rec)
		Expect(got).To(HaveKeyWithValue("name", "todo"))
		Expect(got).To(HaveKeyWithValue("enabled", true))
		Expect(got["hash"]).NotTo(BeEmpty())
		Expect(got["grants"]).To(ConsistOf(map[string]any{"userId": "bob", "displayName": "Bob", "role": "use", "consent": "none"}))

		// A page of someone else, a page that does not exist, a file, pages/
		// itself and a folder inside a page all get the same 403.
		for _, name := range []string{"theirs", "nope", "index.html", "pages", "data", "Todo", "todo%2Fdata"} {
			p := "/api/chats/pages/" + name + "/shares"
			for _, rec := range []*httptest.ResponseRecorder{
				human("alice", http.MethodPut, p, name, map[string]any{"principal": "bob", "role": "view"}),
				human("alice", http.MethodGet, p, name, nil),
				human("alice", http.MethodDelete, p+"/bob", name, nil),
			} {
				Expect(rec.Code).To(Equal(http.StatusForbidden), name+" "+rec.Body.String())
			}
		}
		// The folder routes keep refusing to share anything under pages/. A
		// page that is already shared shows its grants there, so that they can
		// be taken back, and gets no new one.
		rec = s.do("alice", http.MethodPost, "/api/chats/files/shares", map[string]any{"path": "/pages/todo"})
		Expect(rec.Code).To(Equal(http.StatusOK), "the owner sees the grants of a page that is shared")
		id := fmt.Sprint(int64(decodeJSON(rec)["id"].(float64)))
		rec = human("alice", http.MethodPut, "/api/chats/files/shares/"+id+"/grants/root", id, map[string]any{"role": "read"})
		Expect(rec.Code).To(Equal(http.StatusForbidden), "a folder route gives no grant on a page")
		Expect(s.do("alice", http.MethodPost, "/api/chats/files/shares", map[string]any{"path": "/pages/todo/data"}).Code).To(Equal(http.StatusForbidden))
		Expect(exists("/users/alice/pages/todo/index.html")).To(BeTrue())
		_, err := f.Store().Stat(ctx, f.Actor("bob"), "/users/alice/pages/todo/data/items.json")
		Expect(err).NotTo(HaveOccurred(), "bob reads the page's folder")
		_, err = f.Store().Stat(ctx, f.Actor("bob"), "/users/alice/Site/pages/home/index.html")
		Expect(err).To(MatchError(workspace.ErrNotFound), "nothing else of alice's home")
	})

	It("accepts a change, the list and the picker only from the owner's own browser session", func() {
		body := map[string]any{"principal": "bob", "role": "use"}
		Expect(s.do("alice", http.MethodPut, shares, body).Code).To(Equal(http.StatusForbidden), "no confirmation")
		Expect(human("alice", http.MethodPut, shares, "other", body).Code).To(Equal(http.StatusForbidden), "the wrong confirmation")
		Expect(human("alice", http.MethodGet, shares, "", nil).Code).To(Equal(http.StatusForbidden))
		req := s.request("alice", http.MethodPut, shares, body)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("X-LocalAI-Confirm", "todo")
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusForbidden), "an API key")
		Expect(rec.Body.String()).To(ContainSubstring("not_human_surface"))
		req = s.request("alice", http.MethodPut, shares, body)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("X-LocalAI-Confirm", "todo")
		rec = httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusForbidden), "another origin")
		for _, h := range []string{"Mcp-Session-Id", "X-LocalAI-Tool-Call", "X-LocalAI-Agent", "X-LocalAI-Execution-Context"} {
			req = s.request("alice", http.MethodPut, shares, body)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Sec-Fetch-Mode", "cors")
			req.Header.Set("Sec-Fetch-Dest", "empty")
			req.Header.Set("X-LocalAI-Confirm", "todo")
			req.Header.Set(h, "1")
			rec = httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden), "a tool or agent marker: "+h)
		}
		Expect(theirs("bob")).To(BeNil(), "none of the refused calls shared the page")
		Expect(human("alice", http.MethodGet, search+"?q=bob", "todo", nil).Code).To(Equal(http.StatusForbidden), "the picker wants its own word")
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		Expect(theirs("bob")).NotTo(BeNil())
	})

	It("finds people for the picker, with the rules of the folder picker", func() {
		rec := human("alice", http.MethodGet, search+"?q=bo", "pages", nil)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		rec = human("alice", http.MethodGet, search+"?q=slow", "pages", nil)
		Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
		rec = human("alice", http.MethodGet, search+"?q=bob", "pages", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["users"]).To(ConsistOf(map[string]any{"id": "bob", "displayName": "Bob"}))
	})

	It("gives a viewer the page as a shared page of a person, with the owner's name and no path", func() {
		Expect(share("bob", "view").Code).To(Equal(http.StatusOK))
		p := theirs("bob")
		Expect(p).To(HaveKeyWithValue("source", "shared"))
		Expect(p).To(HaveKeyWithValue("user_shared", true))
		Expect(p).To(HaveKeyWithValue("role", "view"))
		Expect(p).NotTo(HaveKey("owner_id"), "the account ID of the owner is not sent")
		Expect(p).To(HaveKeyWithValue("owner_name", "alice"), "the name the studio knows; the account ID when it knows none")
		Expect(p).NotTo(HaveKey("author_id"), "the account ID of the writer is not sent either")
		Expect(p).To(HaveKeyWithValue("author_name", "alice"))
		Expect(p).To(HaveKeyWithValue("consent", "none"))
		Expect(p).NotTo(HaveKey("folder"), "the folder would name alice's account")
		Expect(p).To(HaveKeyWithValue("path", ""))
		Expect(p).NotTo(HaveKey("writable"))
		got := decodeJSON(s.do("bob", http.MethodGet, pageBase("bob"), nil))
		Expect(got["html"]).To(Equal("<p>todo</p>\n"))
		Expect(got["page"]).To(HaveKeyWithValue("user_shared", true))
		Expect(got["consent"]).To(HaveKeyWithValue("state", "none"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-pages", nil))["pages"]).To(BeEmpty(), "the owner's page is not a shared page of the owner")
		Expect(decodeJSON(s.do("root", http.MethodGet, "/api/chats/shared-pages", nil))["pages"]).To(BeEmpty(), "nobody else has it")
		own := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/pages", nil))["pages"].([]any)
		Expect(own).To(ContainElement(And(HaveKeyWithValue("name", "todo"), HaveKeyWithValue("shared_with", BeEquivalentTo(1)))))
		Expect(own).To(ContainElement(And(HaveKeyWithValue("name", "todo"), Not(HaveKey("user_shared")))))
	})

	It("runs view, use and edit against the bridge, the store and Files", func() {
		Expect(share("bob", "view").Code).To(Equal(http.StatusOK))
		hash := allow("bob")
		rec := bridge("bob", http.MethodGet, pageBase("bob")+"/file?path=data/items.json", hash, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["content"]).To(Equal("[]"))
		Expect(write("bob", hash, "data/new.json").Code).To(Equal(http.StatusForbidden), "view cannot save")
		Expect(exists("/users/alice/pages/todo/data/new.json")).To(BeFalse())

		// use: data files through the page and Files, no code anywhere.
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		Expect(write("bob", hash, "data/new.json").Code).To(Equal(http.StatusOK), "the new role holds from the next call")
		Expect(exists("/users/alice/pages/todo/data/new.json")).To(BeTrue(), "it is in the owner's page folder")
		for _, rel := range []string{"app.js", "x.mjs", "style.css", "other.html", "draw.svg", "index.html", "page.json", "data/x.js", "data/Index.HTML"} {
			Expect(write("bob", hash, rel).Code).To(Equal(http.StatusForbidden), rel)
		}
		bob := f.Actor("bob")
		_, err := f.Store().Put(ctx, bob, "/users/alice/pages/todo/data/by-hand.json", strings.NewReader("{}"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred(), "Files may write data")
		for _, p := range []string{"index.html", "page.json", "app.js", "data/x.css"} {
			_, err = f.Store().Put(ctx, bob, "/users/alice/pages/todo/"+p, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
			Expect(err).To(MatchError(workspace.ErrReadOnly), "Files may not write "+p)
		}
		Expect(f.Store().Delete(ctx, bob, "/users/alice/pages/todo/index.html")).To(MatchError(workspace.ErrReadOnly))
		_, err = f.Store().Put(ctx, bob, "/users/alice/pages/other.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrNotFound), "nothing outside the page's folder")

		// A script that the page names is code, whatever its name.
		put("alice", "/users/alice/pages/todo/index.html", `<script src="loader.txt"></script>`)
		hash = allow("bob")
		Expect(write("bob", hash, "loader.txt").Code).To(Equal(http.StatusForbidden), "a code file of the page")

		// edit: Files may change the code, the page still may not.
		Expect(share("bob", "edit").Code).To(Equal(http.StatusOK))
		_, err = f.Store().Put(ctx, bob, "/users/alice/pages/todo/index.html", strings.NewReader("<p>bob wrote this</p>\n"), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
		hash = allow("bob")
		Expect(write("bob", hash, "app.js").Code).To(Equal(http.StatusForbidden), "the bridge never writes code")
		Expect(write("bob", hash, "data/ok.json").Code).To(Equal(http.StatusOK))
	})

	It("keeps the hash rules: a changed page asks again and an old frame reads no new code", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		hash := allow("bob")
		Expect(theirs("bob")).To(HaveKeyWithValue("consent", "allowed"))
		put("alice", "/users/alice/pages/todo/index.html", "<p>changed</p>\n")
		Expect(theirs("bob")).To(HaveKeyWithValue("consent", "changed"))
		rec := bridge("bob", http.MethodGet, pageBase("bob")+"/file?path=index.html", hash, nil)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(bridge("bob", http.MethodGet, pageBase("bob")+"/file?path=data/items.json", hash, nil).Code).To(Equal(http.StatusOK), "an open frame goes on with its data")
		rec = s.do("bob", http.MethodPut, pageBase("bob")+"/consent", map[string]any{"hash": hash})
		Expect(rec.Code).To(Equal(http.StatusConflict), "the old version cannot be allowed now")
		list := grants()
		Expect(list["allowedCount"]).To(BeEquivalentTo(0))
		Expect(list["grants"]).To(ConsistOf(HaveKeyWithValue("consent", "changed")))
		allow("bob")
		Expect(grants()["allowedCount"]).To(BeEquivalentTo(1))
	})

	It("drops the consent when a role is lowered", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		hash := allow("bob")
		Expect(write("bob", hash, "data/a.json").Code).To(Equal(http.StatusOK))
		Expect(share("bob", "view").Code).To(Equal(http.StatusOK))
		Expect(theirs("bob")).To(HaveKeyWithValue("consent", "none"))
		Expect(write("bob", hash, "data/b.json").Code).To(Equal(http.StatusForbidden), "the open frame stops at its next call")
		Expect(exists("/users/alice/pages/todo/data/b.json")).To(BeFalse())
	})

	// ownPage reads alice's own page as her UI does.
	ownPage := func() map[string]any {
		GinkgoHelper()
		rec := s.do("alice", http.MethodGet, "/api/chats/pages/todo", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)
	}
	ownBridge := func(method, rel, hash string, body any) *httptest.ResponseRecorder {
		return bridge("alice", method, "/api/chats/pages/todo/"+rel, hash, body)
	}

	It("leaves the owner's page as it was when it is shared to view or use only", func() {
		Expect(share("bob", "view").Code).To(Equal(http.StatusOK))
		Expect(share("root", "use").Code).To(Equal(http.StatusOK))
		got := ownPage()
		Expect(got["page"]).NotTo(HaveKey("others_can_edit"))
		Expect(got["page"]).To(HaveKey("hash"), "the version the grantees allowed, so the page can say they will be asked again")
		Expect(got).NotTo(HaveKey("consent"))
		Expect(got["page"]).To(HaveKeyWithValue("shared_with", BeEquivalentTo(2)))
		Expect(got["page"]).NotTo(HaveKey("shared_allowed"), "nobody allowed it yet")
		Expect(ownBridge(http.MethodGet, "file?path=data/items.json", "", nil).Code).To(Equal(http.StatusOK), "no consent is asked")
		Expect(human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": "x"}).Code).To(Equal(http.StatusNotFound))
		allow("root")
		Expect(ownPage()["page"]).To(HaveKeyWithValue("shared_allowed", BeEquivalentTo(1)))
	})

	It("takes the consent of an own page only from the owner's own browser session", func() {
		Expect(share("bob", "edit").Code).To(Equal(http.StatusOK))
		hash := ownPage()["page"].(map[string]any)["hash"].(string)
		consent := "/api/chats/pages/todo/consent"
		body := map[string]any{"hash": hash}
		refused := func(rec *httptest.ResponseRecorder, why string) {
			GinkgoHelper()
			Expect(rec.Code).To(Equal(http.StatusForbidden), why)
			Expect(rec.Body.String()).To(ContainSubstring("not_human_surface"), why)
		}
		refused(s.do("alice", http.MethodPut, consent, body), "no confirmation")
		refused(s.do("alice", http.MethodDelete, consent, nil), "no confirmation on a revoke")
		refused(human("alice", http.MethodPut, consent, "other", body), "the wrong confirmation")
		marked := func(method string, mark func(*http.Request)) *httptest.ResponseRecorder {
			req := s.request("alice", method, consent, body)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Sec-Fetch-Mode", "cors")
			req.Header.Set("Sec-Fetch-Dest", "empty")
			req.Header.Set("X-LocalAI-Confirm", "todo")
			mark(req)
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			return rec
		}
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			refused(marked(method, func(r *http.Request) { r.Header.Set("Authorization", "Bearer sk-test") }), method+" with an API key")
			for _, h := range []string{"Mcp-Session-Id", "X-LocalAI-Tool-Call", "X-LocalAI-Agent", "X-LocalAI-Execution-Context"} {
				refused(marked(method, func(r *http.Request) { r.Header.Set(h, "1") }), method+" with the marker "+h)
			}
		}
		Expect(ownPage()["consent"]).To(HaveKeyWithValue("state", "none"), "nothing was recorded")
		Expect(human("alice", http.MethodPut, consent, "todo", body).Code).To(Equal(http.StatusOK))
	})

	It("asks the owner to allow a page that someone may edit, as for a shared page", func() {
		Expect(share("bob", "edit").Code).To(Equal(http.StatusOK))
		own := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/pages", nil))["pages"].([]any)
		Expect(own).To(ConsistOf(And(HaveKeyWithValue("name", "todo"), HaveKeyWithValue("others_can_edit", true), HaveKeyWithValue("consent", "none"), HaveKey("hash"))))
		got := ownPage()
		Expect(got["html"]).To(Equal("<p>todo</p>\n"), "the trust sheet can show the code")
		Expect(got["consent"]).To(HaveKeyWithValue("state", "none"))
		hash := got["page"].(map[string]any)["hash"].(string)
		rec := ownBridge(http.MethodGet, "file?path=data/items.json", hash, nil)
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(rec.Body.String()).To(ContainSubstring("page_not_allowed"))
		Expect(ownBridge(http.MethodGet, "files", "", nil).Code).To(Equal(http.StatusForbidden), "no hash")

		Expect(human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": hash, "embed": true}).Code).To(Equal(http.StatusBadRequest), "no consent for Home or a slot")
		Expect(human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": "stale"}).Code).To(Equal(http.StatusConflict))
		rec = human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": hash})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(ownPage()["consent"]).To(HaveKeyWithValue("state", "allowed"))
		Expect(ownPage()["consent"]).NotTo(HaveKeyWithValue("embed", true))
		Expect(ownBridge(http.MethodGet, "file?path=data/items.json", hash, nil).Code).To(Equal(http.StatusOK))
		Expect(ownBridge(http.MethodPut, "file", hash, map[string]any{"path": "data/mine.json", "content": "{}"}).Code).To(Equal(http.StatusOK))
		Expect(ownBridge(http.MethodPut, "file", hash, map[string]any{"path": "app.js", "content": "x"}).Code).To(Equal(http.StatusForbidden))

		// bob changes the code: alice is asked again, and a frame of the old
		// version reads no new code.
		_, err := f.Store().Put(ctx, f.Actor("bob"), "/users/alice/pages/todo/index.html", strings.NewReader("<p>bob's version</p>\n"), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(ownPage()["consent"]).To(HaveKeyWithValue("state", "changed"))
		Expect(ownBridge(http.MethodGet, "file?path=index.html", hash, nil).Code).To(Equal(http.StatusConflict), "the stale frame")
		Expect(human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": hash}).Code).To(Equal(http.StatusConflict))
		newHash := ownPage()["page"].(map[string]any)["hash"].(string)
		Expect(newHash).NotTo(Equal(hash))
		Expect(human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": newHash, "once": true}).Code).To(Equal(http.StatusOK))

		// A revoke stops the open frame.
		Expect(human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": newHash}).Code).To(Equal(http.StatusOK))
		Expect(human("alice", http.MethodDelete, "/api/chats/pages/todo/consent", "todo", nil).Code).To(Equal(http.StatusNoContent))
		Expect(ownBridge(http.MethodGet, "files", newHash, nil).Code).To(Equal(http.StatusForbidden))

		// Taking back the last edit grant returns the page to the normal path.
		Expect(unshare("bob").Code).To(Equal(http.StatusOK))
		got = ownPage()
		Expect(got["page"]).NotTo(HaveKey("others_can_edit"))
		Expect(got).NotTo(HaveKey("consent"))
		Expect(ownBridge(http.MethodGet, "files", "", nil).Code).To(Equal(http.StatusOK))
		Expect(human("alice", http.MethodPut, "/api/chats/pages/todo/consent", "todo", map[string]any{"hash": newHash}).Code).To(Equal(http.StatusNotFound))
	})

	It("never allows a page that a person shared on Home or in a slot", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		rec := s.do("bob", http.MethodPut, pageBase("bob")+"/consent", map[string]any{"hash": hashOf("bob"), "embed": true})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		Expect(theirs("bob")).To(HaveKeyWithValue("consent", "none"))
		hash := hashOf("bob")
		Expect(s.do("bob", http.MethodPut, pageBase("bob")+"/consent", map[string]any{"hash": hash, "once": true}).Code).To(Equal(http.StatusOK))
		Expect(s.do("bob", http.MethodPut, pageBase("bob")+"/consent", map[string]any{"hash": hash}).Code).To(Equal(http.StatusOK))
	})

	It("gives Everyone view and use, never edit", func() {
		rec := share("@everyone", "edit")
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		Expect(share("bob", "owner").Code).To(Equal(http.StatusBadRequest), "a word that is no role")
		Expect(share("@everyone", "use").Code).To(Equal(http.StatusOK))
		Expect(share("carol-gone", "view").Code).To(Equal(http.StatusBadRequest), "an account that is not one")
		Expect(share("alice", "view").Code).To(Equal(http.StatusBadRequest), "the owner")
		Expect(theirs("bob")).To(HaveKeyWithValue("role", "use"), "every account has it")
		Expect(theirs("root")).NotTo(BeNil())
		list := grants()["grants"].([]any)
		Expect(list).To(ConsistOf(map[string]any{"userId": "@everyone", "displayName": "", "role": "use"}))
		hash := allow("bob")
		Expect(write("bob", hash, "data/b.json").Code).To(Equal(http.StatusOK))
		Expect(share("bob", "edit").Code).To(Equal(http.StatusOK), "a named account may edit")
		Expect(unshare("@everyone").Code).To(Equal(http.StatusOK))
		Expect(theirs("root")).To(BeNil())
		Expect(theirs("bob")).To(HaveKeyWithValue("role", "edit"), "bob keeps his own grant")
	})

	It("takes a revoke at the grantee's next call and drops the consent, so a later grant asks again", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		hash := allow("bob")
		Expect(write("bob", hash, "data/before.json").Code).To(Equal(http.StatusOK))
		base := pageBase("bob")
		rec := unshare("bob")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["grants"]).To(BeEmpty())
		Expect(unshare("bob").Code).To(Equal(http.StatusOK), "safe to repeat")
		Expect(bridge("bob", http.MethodPut, base+"/file", hash, map[string]any{"path": "data/after.json", "content": "{}"}).Code).To(Equal(http.StatusNotFound), "the open frame stops")
		Expect(bridge("bob", http.MethodGet, base+"/file?path=data/items.json", hash, nil).Code).To(Equal(http.StatusNotFound))
		Expect(theirs("bob")).To(BeNil())
		Expect(exists("/users/alice/pages/todo/data/before.json")).To(BeTrue(), "what bob saved stays")
		Expect(exists("/users/alice/pages/todo/data/after.json")).To(BeFalse())
		_, err := f.Store().Stat(ctx, f.Actor("bob"), "/users/alice/pages/todo/data/items.json")
		Expect(err).To(MatchError(workspace.ErrNotFound))

		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		Expect(theirs("bob")).To(HaveKeyWithValue("consent", "none"), "the consent went with the grant")
		Expect(write("bob", hash, "data/again.json").Code).To(Equal(http.StatusForbidden), "the old consent does not come back")
		Expect(exists("/users/alice/pages/todo/data/again.json")).To(BeFalse())
	})

	It("stops every write that starts after a revoke, while calls are in flight", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		hash := allow("bob")
		base := pageBase("bob")
		var (
			revoked atomic.Bool
			late    atomic.Int32
			okay    atomic.Int32
			wg      sync.WaitGroup
			begin   = make(chan struct{})
		)
		for g := range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-begin
				for i := range 60 {
					afterRevoke := revoked.Load()
					rec := bridge("bob", http.MethodPut, base+"/file", hash, map[string]any{"path": fmt.Sprintf("data/g%d-%d.json", g, i), "content": "{}"})
					switch {
					case rec.Code == http.StatusOK && afterRevoke:
						late.Add(1)
					case rec.Code == http.StatusOK:
						okay.Add(1)
					}
				}
			}()
		}
		close(begin)
		for okay.Load() < 3 {
			// let some writes land before the revoke
		}
		Expect(unshare("bob").Code).To(Equal(http.StatusOK))
		revoked.Store(true)
		wg.Wait()
		Expect(late.Load()).To(BeZero(), "a write that began after the revoke succeeded")
		Expect(okay.Load()).To(BeNumerically(">=", 3))
	})

	It("lets two grantees write different files of one page at once", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		Expect(share("root", "use").Code).To(Equal(http.StatusOK))
		hashes := map[string]string{"bob": allow("bob"), "root": allow("root")}
		var wg sync.WaitGroup
		var failed atomic.Int32
		for _, who := range []string{"bob", "root"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 15 {
					rec := write(who, hashes[who], fmt.Sprintf("data/%s-%d.json", who, i))
					if rec.Code != http.StatusOK {
						failed.Add(1)
					}
				}
			}()
		}
		wg.Wait()
		Expect(failed.Load()).To(BeZero())
		for _, who := range []string{"bob", "root"} {
			for i := range 15 {
				Expect(exists(fmt.Sprintf("/users/alice/pages/todo/data/%s-%d.json", who, i))).To(BeTrue(), who)
			}
		}
	})

	It("lets the owner list and take back a share of an account that is gone", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		delete(dir, "bob")
		Expect(share("bob", "view").Code).To(Equal(http.StatusBadRequest), "a removed account cannot be given a role")
		Expect(grants()["grants"]).To(ConsistOf(HaveKeyWithValue("userId", "bob")), "the grant is listed under the account ID")
		rec := unshare("bob")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["grants"]).To(BeEmpty())
		_, err := f.Store().Stat(ctx, f.Actor("bob"), "/users/alice/pages/todo/index.html")
		Expect(err).To(MatchError(workspace.ErrNotFound))
	})

	It("keeps a general share of a home folder from sharing its pages, next to a page share", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/files/shares", map[string]any{"path": "/Site"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		id := fmt.Sprint(int64(decodeJSON(rec)["id"].(float64)))
		Expect(human("alice", http.MethodPut, "/api/chats/files/shares/"+id+"/grants/bob", id, map[string]any{"role": "read"}).Code).To(Equal(http.StatusOK))
		Expect(theirs("bob")).To(BeNil(), "the pages of a shared home folder are no shared pages")
		Expect(share("bob", "view").Code).To(Equal(http.StatusOK))
		list := decodeJSON(s.do("bob", http.MethodGet, "/api/chats/shared-pages", nil))["pages"].([]any)
		Expect(list).To(HaveLen(1))
		Expect(list[0]).To(HaveKeyWithValue("name", "todo"))
		// The same folder routes list the page grant, so the owner sees it in both places.
		owned := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/files/shares", nil))["folders"].([]any)
		Expect(owned).To(ContainElement(HaveKeyWithValue("path", "/pages/todo")))
	})

	It("refuses a new share when an administrator turned page sharing off, and lets the owner take it back", func() {
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		hash := allow("bob")
		sharing.Store(false)
		rec := share("root", "view")
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("sharing_disabled"))
		Expect(human("alice", http.MethodGet, search+"?q=bob", "pages", nil).Code).To(Equal(http.StatusConflict))
		list := grants()
		Expect(list).To(HaveKeyWithValue("enabled", false))
		Expect(list["grants"]).To(HaveLen(1))

		p := theirs("bob")
		Expect(p).To(HaveKeyWithValue("turned_off", true), "listed as turned off")
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/shared-pages", nil))).To(HaveKeyWithValue("user_shares_enabled", false))
		base := fmt.Sprintf("/api/chats/shared-pages/%d/todo", int64(p["folder_id"].(float64)))
		rec = s.do("bob", http.MethodGet, base, nil)
		Expect(rec.Code).To(Equal(http.StatusForbidden), "it does not open")
		Expect(rec.Body.String()).To(ContainSubstring("user_page_shares_off"))
		rec = bridge("bob", http.MethodPut, base+"/file", hash, map[string]any{"path": "data/x.json", "content": "{}"})
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(rec.Body.String()).To(ContainSubstring("user_page_shares_off"))
		Expect(exists("/users/alice/pages/todo/data/x.json")).To(BeFalse())

		Expect(unshare("bob").Code).To(Equal(http.StatusOK), "the owner can still revoke")
		Expect(theirs("bob")).To(BeNil())
		sharing.Store(true)
		Expect(share("bob", "use").Code).To(Equal(http.StatusOK))
		allow("bob")
	})

	It("says sharing is off on a server without accounts", func() {
		plain := newStudioServerWith(newTestFiles(), nil)
		for _, call := range []func() *httptest.ResponseRecorder{
			func() *httptest.ResponseRecorder {
				return plainHuman(plain, "alice", http.MethodGet, shares, "todo", nil)
			},
			func() *httptest.ResponseRecorder {
				return plainHuman(plain, "alice", http.MethodPut, shares, "todo", map[string]any{"principal": "bob", "role": "view"})
			},
			func() *httptest.ResponseRecorder {
				return plainHuman(plain, "alice", http.MethodGet, search+"?q=bob", "pages", nil)
			},
		} {
			rec := call()
			Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("sharing_disabled"))
		}
	})
})

// plainHuman is human for another test server.
func plainHuman(s *studioServer, user, method, path, confirm string, body any) *httptest.ResponseRecorder {
	req := s.request(user, method, path, body)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("X-LocalAI-Confirm", confirm)
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}
