// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The pages of a shared folder /shared/team that "root" writes: alice may read
// the folder, bob may write it, dave has the pages feature off and carol has no
// chats. A second page "copy" has the same code as "standup".
var _ = Describe("Chats shared pages endpoints", func() {
	var (
		s      *studioServer
		f      *files.Service
		on     atomic.Bool
		folder string
		base   string
	)
	write := func(p, body string) {
		_, err := f.Store().Put(context.Background(), workspace.Actor{Principal: "root"}, p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	// bridge calls a route of the bridge with the headers of a frame.
	bridge := func(user, method, path, hash, visit string, body any) *httptest.ResponseRecorder {
		req := s.request(user, method, path, body)
		if hash != "" {
			req.Header.Set("X-LocalAI-Page-Hash", hash)
		}
		if visit != "" {
			req.Header.Set("X-LocalAI-Page-Visit", visit)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	get := func(user string) map[string]any {
		rec := s.do(user, http.MethodGet, base, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)
	}

	BeforeEach(func() {
		ctx := context.Background()
		f = newTestFiles()
		on.Store(true)
		s = newStudioServerWith(f, func(d *studio.Deps) { d.SharedPagesOn = on.Load })
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		for _, u := range []string{"alice", "bob", "root"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		n, err := f.Store().Mkdir(ctx, admin, "/shared/team/pages")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Store().Grant(ctx, admin, "root", "/shared/team", workspace.ModeWrite)).To(Succeed())
		Expect(f.Store().Grant(ctx, admin, "alice", "/shared/team", workspace.ModeRead)).To(Succeed())
		Expect(f.Store().Grant(ctx, admin, "bob", "/shared/team", workspace.ModeWrite)).To(Succeed())
		Expect(n.ID).NotTo(BeZero())
		write("/shared/team/pages/standup/index.html", "<p>standup</p>\n")
		write("/shared/team/pages/standup/page.json", `{"title":"Team standup"}`)
		write("/shared/team/pages/standup/data.json", `{"n":1}`)
		write("/shared/team/pages/copy/index.html", "<p>standup</p>\n")
		write("/shared/team/pages/copy/page.json", `{"title":"Team standup"}`)
		write("/shared/team/notes.txt", "team notes")
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-pages", nil))["pages"].([]any)
		Expect(list).To(HaveLen(2))
		folder = fmt.Sprint(int64(list[0].(map[string]any)["folder_id"].(float64)))
		base = "/api/chats/shared-pages/" + folder + "/standup"
	})

	It("lists the shared pages with their author, folder and consent, and opens one", func() {
		rec := s.do("alice", http.MethodGet, "/api/chats/shared-pages", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := decodeJSON(rec)
		Expect(body["enabled"]).To(BeTrue())
		p := body["pages"].([]any)[1].(map[string]any)
		Expect(p).To(HaveKeyWithValue("name", "standup"))
		Expect(p).To(HaveKeyWithValue("source", "shared"))
		Expect(p).To(HaveKeyWithValue("folder", "/shared/team"))
		Expect(p).To(HaveKeyWithValue("author_id", "root"))
		Expect(p).To(HaveKeyWithValue("author_name", "root"))
		Expect(p).To(HaveKeyWithValue("consent", "none"))
		Expect(p).NotTo(HaveKey("writable"), "alice can only read")
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/pages", nil))["pages"]).To(BeEmpty(), "shared pages are not own pages")

		got := get("alice")
		Expect(got["html"]).To(Equal("<p>standup</p>\n"))
		Expect(got["lines"]).To(BeNumerically("==", 2))
		Expect(got["consent"]).To(HaveKeyWithValue("state", "none"))
	})

	It("a viewer without a grant sees nothing, and the routes need the features", func() {
		root := decodeJSON(s.do("root", http.MethodGet, "/api/chats/shared-pages", nil))
		Expect(root["pages"]).To(HaveLen(2), "root has a grant too")
		Expect(f.EnsureHome(context.Background(), "dave")).To(Succeed())
		Expect(s.do("dave", http.MethodGet, "/api/chats/shared-pages", nil).Code).To(Equal(http.StatusForbidden), "the pages feature is off for dave")
		Expect(s.do("carol", http.MethodGet, base, nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("", http.MethodGet, base, nil).Code).To(Equal(http.StatusUnauthorized))
		// A user with the feature and no grant: nothing, and every route is 404.
		ctx := context.Background()
		Expect(f.Store().Revoke(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "bob", "/shared/team")).To(Succeed())
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/shared-pages", nil))["pages"]).To(BeEmpty())
		for _, path := range []string{base, base + "/files", base + "/file?path=data.json"} {
			Expect(bridge("bob", http.MethodGet, path, "x", "", nil).Code).To(Equal(http.StatusNotFound), path)
		}
		Expect(s.do("bob", http.MethodPut, base+"/consent", map[string]any{"hash": "x"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/shared-pages/notanumber/standup", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/shared-pages/1/standup", nil).Code).To(Equal(http.StatusNotFound), "a folder id that is not one of alice's")
	})

	It("the bridge needs a consent to the version that runs, and a revoke stops it at the next call", func() {
		hash := get("alice")["page"].(map[string]any)["hash"].(string)
		Expect(bridge("alice", http.MethodGet, base+"/file?path=data.json", hash, "", nil).Code).To(Equal(http.StatusForbidden))
		rec := bridge("alice", http.MethodGet, base+"/files", "", "", nil)
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(rec.Body.String()).To(ContainSubstring("page_not_allowed"))

		Expect(s.do("alice", http.MethodPut, base+"/consent", map[string]any{"hash": "stale"}).Code).To(Equal(http.StatusConflict))
		rec = s.do("alice", http.MethodPut, base+"/consent", map[string]any{"hash": hash})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("state", "allowed"))
		Expect(get("alice")["consent"]).To(HaveKeyWithValue("state", "allowed"))

		rec = bridge("alice", http.MethodGet, base+"/file?path=data.json", hash, "", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["content"]).To(Equal(`{"n":1}`))
		// The consent of standup does not open copy, which has the same code.
		Expect(bridge("alice", http.MethodGet, "/api/chats/shared-pages/"+folder+"/copy/file?path=index.html", hash, "", nil).Code).To(Equal(http.StatusForbidden))
		// Nor bob's frame.
		Expect(bridge("bob", http.MethodGet, base+"/file?path=data.json", hash, "", nil).Code).To(Equal(http.StatusForbidden))

		// An image read for the frame needs the same consent.
		write("/shared/team/pages/standup/logo.png", "\x89PNG")
		rec = bridge("alice", http.MethodGet, base+"/file?encoding=base64&path=logo.png", hash, "", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["media_type"]).To(Equal("image/png"))
		Expect(bridge("alice", http.MethodGet, base+"/file?encoding=base64&path=logo.png", "", "", nil).Code).To(Equal(http.StatusForbidden))
		Expect(bridge("alice", http.MethodGet, base+"/file?encoding=base64&path="+q("../../notes.txt"), hash, "", nil).Code).To(Equal(http.StatusBadRequest))

		Expect(s.do("alice", http.MethodDelete, base+"/consent", nil).Code).To(Equal(http.StatusNoContent))
		Expect(bridge("alice", http.MethodGet, base+"/file?path=data.json", hash, "", nil).Code).To(Equal(http.StatusForbidden), "a revoked consent")
		Expect(bridge("alice", http.MethodGet, base+"/file?encoding=base64&path=logo.png", hash, "", nil).Code).To(Equal(http.StatusForbidden), "a revoked consent")
		Expect(get("alice")["consent"]).To(HaveKeyWithValue("state", "none"))
	})

	It("a changed page asks again and says what was allowed; allow once is a visit", func() {
		p := get("alice")["page"].(map[string]any)
		hash := p["hash"].(string)
		Expect(s.do("alice", http.MethodPut, base+"/consent", map[string]any{"hash": hash}).Code).To(Equal(http.StatusOK))
		write("/shared/team/pages/standup/index.html", "<p>standup v2</p>\n<p>more</p>\n")
		got := get("alice")
		Expect(got["page"].(map[string]any)["consent"]).To(Equal("changed"))
		c := got["consent"].(map[string]any)
		Expect(c).To(HaveKeyWithValue("state", "changed"))
		Expect(c["size"]).To(BeNumerically("==", len("<p>standup</p>\n")))
		Expect(c["lines"]).To(BeNumerically("==", 2))
		Expect(c).To(HaveKey("allowed_at"))
		Expect(got["lines"]).To(BeNumerically("==", 3))
		newHash := got["page"].(map[string]any)["hash"].(string)
		Expect(bridge("alice", http.MethodGet, base+"/files", hash, "", nil).Code).To(Equal(http.StatusOK), "the open version keeps running")
		Expect(bridge("alice", http.MethodGet, base+"/files", newHash, "", nil).Code).To(Equal(http.StatusForbidden))

		rec := s.do("alice", http.MethodPut, base+"/consent", map[string]any{"hash": newHash, "once": true})
		Expect(rec.Code).To(Equal(http.StatusOK))
		once := decodeJSON(rec)
		Expect(once).To(HaveKeyWithValue("kind", "once"))
		token := once["token"].(string)
		Expect(bridge("alice", http.MethodGet, base+"/files", newHash, token, nil).Code).To(Equal(http.StatusOK))
		Expect(get("alice")["consent"]).To(HaveKeyWithValue("state", "changed"), "allow once keeps asking")
	})

	It("a shared page writes with the viewer's own grant", func() {
		hash := get("alice")["page"].(map[string]any)["hash"].(string)
		for _, u := range []string{"alice", "bob"} {
			Expect(s.do(u, http.MethodPut, base+"/consent", map[string]any{"hash": hash}).Code).To(Equal(http.StatusOK))
		}
		rec := bridge("alice", http.MethodPut, base+"/file", hash, "", map[string]any{"path": "data.json", "content": `{"n":2}`})
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("you can only read this shared folder"))
		rec = bridge("bob", http.MethodPut, base+"/file", hash, "", map[string]any{"path": "data.json", "content": `{"n":2}`})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		for _, bad := range []string{"../notes.txt", "../../notes.txt", "/shared/team/notes.txt", "a/../../notes.txt"} {
			Expect(bridge("bob", http.MethodGet, base+"/file?path="+q(bad), hash, "", nil).Code).To(Equal(http.StatusBadRequest), bad)
			Expect(bridge("bob", http.MethodPut, base+"/file", hash, "", map[string]any{"path": bad, "content": "x"}).Code).To(Equal(http.StatusBadRequest), bad)
		}
		for _, alias := range []string{"INDEX.HTML", "Page.Json"} {
			Expect(bridge("bob", http.MethodPut, base+"/file", hash, "", map[string]any{"path": alias, "content": "x"}).Code).To(Equal(http.StatusForbidden), alias)
		}
		Expect(bridge("bob", http.MethodGet, "/api/chats/shared-pages/"+folder+"/STANDUP/files", hash, "", nil).Code).To(Equal(http.StatusBadRequest))
	})

	It("an administrator's switch turns shared pages off for the list, the page and the bridge", func() {
		hash := get("alice")["page"].(map[string]any)["hash"].(string)
		Expect(s.do("alice", http.MethodPut, base+"/consent", map[string]any{"hash": hash}).Code).To(Equal(http.StatusOK))
		on.Store(false)
		body := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/shared-pages", nil))
		Expect(body["enabled"]).To(BeFalse())
		for _, rec := range []*httptest.ResponseRecorder{
			s.do("alice", http.MethodGet, base, nil),
			bridge("alice", http.MethodGet, base+"/files", hash, "", nil),
			bridge("alice", http.MethodGet, base+"/file?path=data.json", hash, "", nil),
			s.do("alice", http.MethodPut, base+"/consent", map[string]any{"hash": hash}),
		} {
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(rec.Body.String()).To(ContainSubstring("shared_pages_off"))
		}
		Expect(s.do("alice", http.MethodDelete, base+"/consent", nil).Code).To(Equal(http.StatusNoContent), "a revoke works with shared pages off")
		on.Store(true)
		Expect(bridge("alice", http.MethodGet, base+"/files", hash, "", nil).Code).To(Equal(http.StatusForbidden))
	})
})
