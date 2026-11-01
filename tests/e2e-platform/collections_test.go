package e2eplatform_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const mockEmbedModel = "name: mock-embed\nbackend: mock-backend\nembeddings: true\nparameters:\n  model: mock-embed.bin\n"

// waitCollections waits until the collections service has started: /readyz
// answers before it, and the routes answer 503 meanwhile.
func waitCollections(a *api) {
	Eventually(func() int {
		code, _ := a.do(http.MethodGet, "/api/collections", nil)
		return code
	}, 60*time.Second, 250*time.Millisecond).ShouldNot(Equal(http.StatusServiceUnavailable))
}

// upload sends a text file to a collection.
func upload(a *api, base, name, filename, content string) (int, []byte) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	Expect(err).ToNot(HaveOccurred())
	_, _ = fw.Write([]byte(content))
	Expect(mw.Close()).To(Succeed())
	code, out, _ := a.doH(http.MethodPost, base+"/"+name+"/upload", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	return code, out
}

// Collections (the knowledge base, built on localrecall) on the real binary.
// The embedding model is the mock backend, which returns one fixed vector, so
// what is checked is the plumbing: storage, entries, search results, sources,
// the permission and the names of the routes and options.
var _ = Describe("Collections", Label("platform", "collections"), Ordered, func() {
	var (
		in     *instance
		admin  *api
		bob    *api
		bobID  string
		oldKey string
	)

	BeforeAll(func() {
		in = startInstance(startOpts{Name: "collections", Auth: true,
			Models: map[string]string{"mock-embed.yaml": mockEmbedModel},
			Args:   []string{"--collections-embedding-model", "mock-embed", "--api-keys", "e2e-static-key"}})
		admin, _ = signUp(in, "admin@e2e.test")
		bob, bobID = signUp(in, "bob@e2e.test")
		waitCollections(admin)
	})

	It("embeds an upload when authentication is on and no static API key is set", func() {
		bare := startInstance(startOpts{Name: "collections-bare", Auth: true,
			Models: map[string]string{"mock-embed.yaml": mockEmbedModel},
			Args:   []string{"--collections-embedding-model", "mock-embed"}})
		a, _ := signUp(bare, "admin@e2e.test")
		waitCollections(a)
		a.mustJSON(http.MethodPost, "/api/collections", obj{"name": "notes"}, http.StatusCreated)
		code, out := upload(a, "/api/collections", "notes", "a.txt", "hello")
		Expect(code).To(Equal(http.StatusOK), string(out))
	})

	It("creates a collection, takes an upload, lists the entry and its content, and finds it by search", func() {
		admin.mustJSON(http.MethodPost, "/api/collections", obj{"name": "notes"}, http.StatusCreated)
		list := admin.mustJSON(http.MethodGet, "/api/collections", nil, http.StatusOK)
		Expect(list["collections"]).To(ContainElement("notes"))

		code, out := upload(admin, "/api/collections", "notes", "garden.txt", "Tomatoes need full sun and regular watering. Basil grows well beside them.")
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(decode(out)).To(HaveKeyWithValue("filename", "garden.txt"))
		key := decode(out)["key"].(string)

		entries := admin.mustJSON(http.MethodGet, "/api/collections/notes/entries", nil, http.StatusOK)
		Expect(entries["entries"]).To(ContainElement(key))
		Expect(key).To(HaveSuffix("garden.txt"))
		content := admin.mustJSON(http.MethodGet, "/api/collections/notes/entries/"+key, nil, http.StatusOK)
		Expect(fmt.Sprint(content["content"])).To(ContainSubstring("Tomatoes"))
		Expect(content["chunk_count"]).To(BeNumerically(">=", 1))

		res := admin.mustJSON(http.MethodPost, "/api/collections/notes/search", obj{"query": "basil", "max_results": 1}, http.StatusOK)
		Expect(res["count"]).To(BeNumerically(">=", 1))
		Expect(fmt.Sprint(res["results"])).To(ContainSubstring("Basil"))

		code, raw := admin.do(http.MethodGet, "/api/collections/notes/entries-raw/"+key, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(raw)).To(ContainSubstring("Tomatoes"))
	})

	It("searches a collection that has fewer passages than the number asked for", func() {
		admin.mustJSON(http.MethodPost, "/api/collections", obj{"name": "small"}, http.StatusCreated)
		for _, n := range []int{0, 5, 20} {
			code, out := admin.do(http.MethodPost, "/api/collections/small/search", obj{"query": "anything", "max_results": n})
			Expect(code).To(Equal(http.StatusOK), fmt.Sprintf("max_results=%d: %s", n, out))
		}
		code, out := upload(admin, "/api/collections", "small", "one.txt", "one short passage")
		Expect(code).To(Equal(http.StatusOK), string(out))
		chat := admin.newChat(nil)
		msgs := admin.turn(chat, callMarker("search_collection", obj{"collection": "small", "query": "passage"})+" go")
		Expect(anyContains(toolResults(msgs), "the search failed")).To(BeFalse(), fmt.Sprint(toolResults(msgs)))
	})

	It("lists, adds and removes an external source", func() {
		admin.mustJSON(http.MethodPost, "/api/collections/notes/sources", obj{"url": "https://example.invalid/feed", "update_interval": 60}, http.StatusOK)
		src := admin.mustJSON(http.MethodGet, "/api/collections/notes/sources", nil, http.StatusOK)
		Expect(fmt.Sprint(src["sources"])).To(ContainSubstring("example.invalid"))
		code, out := admin.do(http.MethodDelete, "/api/collections/notes/sources", obj{"url": "https://example.invalid/feed"})
		Expect(code).To(Equal(http.StatusOK), string(out))
		src = admin.mustJSON(http.MethodGet, "/api/collections/notes/sources", nil, http.StatusOK)
		Expect(src["count"]).To(BeZero())
	})

	It("answers 404 for a collection that does not exist", func() {
		for _, r := range [][2]string{{http.MethodGet, "/api/collections/nope/entries"}, {http.MethodGet, "/api/collections/nope/sources"}} {
			code, _ := admin.do(r[0], r[1], nil)
			Expect(code).To(Equal(http.StatusNotFound), r[1])
		}
		code, _ := admin.do(http.MethodPost, "/api/collections/nope/search", obj{"query": "x"})
		Expect(code).To(Equal(http.StatusNotFound))
		code, _ = upload(admin, "/api/collections", "nope", "a.txt", "x")
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("serves the deprecated /api/agents/collections path with a Deprecation header, and only that path", func() {
		code, out, hdr := admin.doH(http.MethodGet, "/api/agents/collections", nil, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(hdr.Get("Deprecation")).To(Equal("true"))
		Expect(hdr.Get("Link")).To(ContainSubstring("/api/collections"))
		Expect(decode(out)["collections"]).To(ContainElement("notes"))
		// The same store: an entry made by the old path is seen on the new one.
		code, out = upload(admin, "/api/agents/collections", "notes", "via-old.txt", "uploaded through the old path")
		Expect(code).To(Equal(http.StatusOK), string(out))
		oldKey = decode(out)["key"].(string)
		Expect(admin.mustJSON(http.MethodGet, "/api/collections/notes/entries", nil, http.StatusOK)["entries"]).To(ContainElement(oldKey))
		_, _, canon := admin.doH(http.MethodGet, "/api/collections", nil, nil)
		Expect(canon.Get("Deprecation")).To(BeEmpty())
	})

	It("deletes an entry and resets a collection", func() {
		code, out := admin.do(http.MethodDelete, "/api/collections/notes/entry/delete", obj{"entry": oldKey})
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(decode(out)["remaining_entries"]).NotTo(ContainElement(oldKey))
		admin.mustJSON(http.MethodPost, "/api/collections/notes/reset", nil, http.StatusOK)
		Expect(admin.mustJSON(http.MethodGet, "/api/collections", nil, http.StatusOK)["collections"]).To(ContainElement("notes"))
		Expect(admin.mustJSON(http.MethodGet, "/api/collections/notes/entries", nil, http.StatusOK)["count"]).To(BeZero())
	})

	It("needs the collections feature, and keeps each user's collections apart", func() {
		for _, p := range []string{"/api/collections", "/api/agents/collections"} {
			code, _ := bob.do(http.MethodGet, p, nil)
			Expect(code).To(Equal(http.StatusForbidden), "an ordinary user reached "+p+" without the feature")
		}
		admin.mustJSON(http.MethodPut, "/api/auth/admin/users/"+bobID+"/permissions", obj{"collections": true}, http.StatusOK)
		bob.mustJSON(http.MethodPost, "/api/collections", obj{"name": "bobs"}, http.StatusCreated)
		bobList := bob.mustJSON(http.MethodGet, "/api/collections", nil, http.StatusOK)
		Expect(bobList["collections"]).To(ConsistOf("bobs"), "a user sees only their own collections")
		code, _ := bob.do(http.MethodGet, "/api/collections/notes/entries", nil)
		Expect(code).To(Equal(http.StatusNotFound))
		adminList := admin.mustJSON(http.MethodGet, "/api/collections", nil, http.StatusOK)
		Expect(fmt.Sprint(adminList["collections"])).NotTo(ContainSubstring("bobs"))
	})

	It("refuses the routes without a credential", func() {
		anon := newAPI(in.api.base)
		for _, p := range []string{"/api/collections", "/api/agents/collections"} {
			code, _ := anon.do(http.MethodGet, p, nil)
			Expect(code).To(Equal(http.StatusUnauthorized), p)
		}
	})
})

// The names of the options: the collections settings, and the old agent-pool
// names that still work with one warning each.
var _ = Describe("Collections options", Label("platform", "collections"), func() {
	withEmbed := func(name string, env map[string]string, args ...string) *instance {
		in := startInstance(startOpts{Name: name, Env: env, Models: map[string]string{"mock-embed.yaml": mockEmbedModel},
			Args: append([]string{"--collections-embedding-model", "mock-embed"}, args...)})
		waitCollections(in.api)
		return in
	}
	warnings := func(in *instance, old string) int {
		n := 0
		for _, l := range strings.Split(in.logs(), "\n") {
			if strings.Contains(l, "Deprecated option used") && strings.Contains(l, `old="`+old+`"`) {
				n++
			}
		}
		return n
	}
	hasEntryFiles := func(dir string) bool {
		found := false
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				found = true
			}
			return nil
		})
		return found
	}

	It("keeps the data in LOCALAI_COLLECTIONS_STATE_DIR and logs no deprecation", func() {
		dir := filepath.Join(workDir, "col-new-state")
		Expect(os.RemoveAll(dir)).To(Succeed())
		in := withEmbed("col-new", map[string]string{"LOCALAI_COLLECTIONS_STATE_DIR": dir})
		in.api.mustJSON(http.MethodPost, "/api/collections", obj{"name": "c1"}, http.StatusCreated)
		code, out := upload(in.api, "/api/collections", "c1", "a.txt", "hello collections")
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(hasEntryFiles(dir)).To(BeTrue(), "the collection did not use the state dir")
		Expect(in.logs()).NotTo(ContainSubstring("Deprecated option used"))
	})

	It("still takes the old LOCALAI_AGENT_POOL_* names, with one warning for each", func() {
		dir := filepath.Join(workDir, "col-old-state")
		Expect(os.RemoveAll(dir)).To(Succeed())
		in := withEmbed("col-old", map[string]string{"LOCALAI_AGENT_POOL_STATE_DIR": dir, "LOCALAI_AGENT_POOL_CHUNK_OVERLAP": "10"})
		in.api.mustJSON(http.MethodPost, "/api/collections", obj{"name": "c1"}, http.StatusCreated)
		code, out := upload(in.api, "/api/collections", "c1", "a.txt", "hello old names")
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(hasEntryFiles(dir)).To(BeTrue(), "the old state dir name was ignored")
		Expect(warnings(in, "LOCALAI_AGENT_POOL_STATE_DIR")).To(Equal(1), in.logs())
		Expect(warnings(in, "LOCALAI_AGENT_POOL_CHUNK_OVERLAP")).To(Equal(1))
	})

	It("lets the new name win when both are set", func() {
		oldDir, newDir := filepath.Join(workDir, "col-both-old"), filepath.Join(workDir, "col-both-new")
		Expect(os.RemoveAll(oldDir)).To(Succeed())
		Expect(os.RemoveAll(newDir)).To(Succeed())
		in := withEmbed("col-both", map[string]string{"LOCALAI_AGENT_POOL_STATE_DIR": oldDir, "LOCALAI_COLLECTIONS_STATE_DIR": newDir})
		in.api.mustJSON(http.MethodPost, "/api/collections", obj{"name": "c1"}, http.StatusCreated)
		code, out := upload(in.api, "/api/collections", "c1", "a.txt", "hello both names")
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(hasEntryFiles(newDir)).To(BeTrue(), "the new name did not win")
		Expect(hasEntryFiles(oldDir)).To(BeFalse(), "the old name was used although the new one was set")
	})

	It("switches the whole API off with --disable-collections and with the old LOCALAI_DISABLE_AGENTS", func() {
		off := startInstance(startOpts{Name: "col-off", Args: []string{"--disable-collections"}})
		for _, p := range []string{"/api/collections", "/api/agents/collections"} {
			code, _ := off.api.do(http.MethodGet, p, nil)
			Expect(code).To(Equal(http.StatusNotFound), p)
		}
		off.stop()
		old := startInstance(startOpts{Name: "col-off-old", Env: map[string]string{"LOCALAI_DISABLE_AGENTS": "true"}})
		code, _ := old.api.do(http.MethodGet, "/api/collections", nil)
		Expect(code).To(Equal(http.StatusNotFound))
		Expect(warnings(old, "LOCALAI_DISABLE_AGENTS")).To(Equal(1), old.logs())
		_ = time.Second
	})
})
