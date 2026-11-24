// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// digestOf is the bare hex digest of the package "notes" currently installed.
func digestOf(s *appsStack, id string) string {
	st, err := s.manager.Status(context.Background(), id)
	Expect(err).NotTo(HaveOccurred())
	for _, g := range st.Generations {
		if g.Number == st.Generation {
			return strings.TrimPrefix(g.Receipt.Digest, "sha256:")
		}
	}
	Fail("no current generation")
	return ""
}

func getAsset(e http.Handler, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

var _ = Describe("immutable add-on assets", Label("assets"), func() {
	var (
		s      *appsStack
		digest string
		base   string
	)
	BeforeEach(func() {
		s = newAppsStack()
		digest = digestOf(s, "notes")
		base = "/api/addons/notes/assets/" + digest + "/"
	})

	It("serves the declared files of the current package through every frontend with immutable headers", func() {
		s.each(func(name string, e http.Handler) {
			js := getAsset(e, base+"ui.js", bearerOf(s.tokens["carol"]))
			Expect(js.Code).To(Equal(200), name)
			Expect(js.Body.String()).To(Equal("export default {};"), name)
			Expect(js.Header().Get("Content-Type")).To(Equal("text/javascript"), name)
			Expect(js.Header().Get("Cache-Control")).To(Equal("private, max-age=31536000, immutable"), name)
			Expect(js.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"), name)
			Expect(js.Header().Get("Content-Disposition")).To(BeEmpty(), name)
			css := getAsset(e, base+"ui/style.css", bearerOf(s.tokens["carol"]))
			Expect(css.Code).To(Equal(200), name)
			Expect(css.Body.String()).To(Equal(".notes{color:red}"), name)
			Expect(css.Header().Get("Content-Type")).To(Equal("text/css"), name)
			Expect(css.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"), name)
			// Administrators read the same bytes.
			Expect(getAsset(e, base+"ui.js", bearerOf(s.tokens["alice"])).Code).To(Equal(200), name)
		})
	})

	It("lists the same URLs the route serves, and only for an enabled addon", func() {
		s.each(func(name string, e http.Handler) {
			list := send(e, "GET", "/api/addons/apps", "", bearerOf(s.tokens["carol"]))
			ui, _ := appsOf(list)["notes"]["ui"].(map[string]any)
			Expect(ui).NotTo(BeNil(), name)
			Expect(ui["entry"]).To(Equal(base+"ui.js"), name)
			Expect(ui["css"]).To(Equal([]any{base + "ui/style.css"}), name)
			Expect(ui["sdk"]).To(Equal(map[string]any{"min": float64(1), "max": float64(1)}), name)
			Expect(ui["pages"]).To(Equal([]any{"home"}))
			Expect(ui["navigation"]).To(Equal([]any{"home"}))
			Expect(ui["chatActions"]).To(Equal([]any{"summarize"}))
			Expect(getAsset(e, ui["entry"].(string), bearerOf(s.tokens["carol"])).Code).To(Equal(200), name)
			// A headless addon has no UI entry.
			admin := send(e, "GET", "/api/addons/apps", "", bearerOf(s.tokens["alice"]))
			Expect(appsOf(admin)["headless"]).NotTo(HaveKey("ui"), name)
		})
	})

	It("denies every other path, digest, addon and credential form with one answer and no immutable caching", func() {
		s.each(func(name string, e http.Handler) {
			var denial []byte
			headless := digestOf(s, "headless")
			for _, path := range []string{
				base + "secret.txt", base + "manifest.json", base + "bin/sample", base + "ui/icon.png",
				base + "../manifest.json", base + "..%2Fmanifest.json", base + "%2e%2e/manifest.json", base + "ui/../ui.js",
				base + "ui%2F..%2F..%2Fmanifest.json", base + "./ui.js", base + "ui.js/", base + "ui/", base, base + "UI.JS", base + "ui.js%00.css",
				"/api/addons/notes/assets/" + strings.Repeat("0", 64) + "/ui.js",
				"/api/addons/notes/assets/" + strings.ToUpper(digest) + "/ui.js",
				"/api/addons/notes/assets/sha256:" + digest + "/ui.js",
				"/api/addons/notes/assets/" + digest[:63] + "/ui.js",
				"/api/addons/headless/assets/" + digest + "/ui.js",
				"/api/addons/headless/assets/" + headless + "/ui.js",
				"/api/addons/missing/assets/" + digest + "/ui.js",
				"/api/addons/..%2Fnotes/assets/" + digest + "/ui.js",
			} {
				w := getAsset(e, path, bearerOf(s.tokens["carol"]))
				Expect(w.Code).To(Equal(403), name+" "+path)
				Expect(w.Header().Get("Cache-Control")).NotTo(ContainSubstring("immutable"), name+" "+path)
				Expect(w.Header().Get("Content-Type")).NotTo(ContainSubstring("javascript"), name+" "+path)
				Expect(w.Body.String()).NotTo(ContainSubstring("export default"), name+" "+path)
				Expect(w.Body.String()).NotTo(ContainSubstring("owner only"), name+" "+path)
				if denial == nil {
					denial = w.Body.Bytes()
				}
				Expect(w.Body.Bytes()).To(Equal(denial), name+" "+path)
			}
			// Authentication and the feature come first.
			Expect(getAsset(e, base+"ui.js", nil).Code).To(Equal(401), name)
			Expect(getAsset(e, base+"ui.js", func(r *http.Request) { forgedHeaders(r) }).Code).To(Equal(401), name)
			Expect(getAsset(e, base+"ui.js", bearerOf(s.tokens["dave"])).Code).To(Equal(403), name+": no addons feature")
			// A credential in the URL is never authority.
			for _, q := range []string{"?token=" + s.tokens["carol"], "?api_key=" + s.tokens["carol"], "?access_token=" + s.tokens["carol"], "?session=" + s.cookies["carol"]} {
				Expect(getAsset(e, base+"ui.js"+q, nil).Code).To(Equal(401), name+" "+q)
			}
		})
	})

	It("accepts a same-origin browser session and refuses a cross-site or direct one", func() {
		s.each(func(name string, e http.Handler) {
			browser := func(site string) func(*http.Request) {
				return func(r *http.Request) {
					r.AddCookie(&http.Cookie{Name: "session", Value: s.cookies["carol"]})
					if site != "" {
						r.Header.Set("Sec-Fetch-Site", site)
						r.Header.Set("Sec-Fetch-Mode", "cors")
						r.Header.Set("Sec-Fetch-Dest", "script")
					}
				}
			}
			ok := getAsset(e, base+"ui.js", browser("same-origin"))
			Expect(ok.Code).To(Equal(200), name)
			Expect(ok.Header().Get("Cache-Control")).To(Equal("private, max-age=31536000, immutable"), name)
			for _, site := range []string{"cross-site", "same-site", "none", ""} {
				w := getAsset(e, base+"ui.js", browser(site))
				Expect(w.Code).To(Equal(403), name+" Sec-Fetch-Site "+site)
				Expect(w.Body.String()).NotTo(ContainSubstring("export default"))
			}
		})
	})

	It("withdraws the code the moment the addon is disabled, uninstalled or replaced, even for a page that is still open", func() {
		ctx := context.Background()
		get := func(who string) int {
			return getAsset(s.frontends["front-a"], base+"ui.js", bearerOf(s.tokens[who])).Code
		}
		Expect(get("carol")).To(Equal(200))
		_, err := s.manager.Submit(ctx, s.owner, managerpkg.LifecycleCommand{OperationID: "off", AddonID: "notes", Action: "disable", Generation: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(get("carol")).To(Equal(403))
		Expect(get("alice")).To(Equal(403), "an administrator is denied the code of a disabled addon too")
		// The list says the same: no UI entry for anyone.
		admin := send(s.frontends["owner"], "GET", "/api/addons/apps", "", bearerOf(s.tokens["alice"]))
		Expect(appsOf(admin)["notes"]).NotTo(HaveKey("ui"))
		_, err = s.manager.Submit(ctx, s.owner, managerpkg.LifecycleCommand{OperationID: "on", AddonID: "notes", Action: "enable", Generation: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(get("carol")).To(Equal(200), "the same immutable URL serves again once the same package is enabled")
		_, err = s.manager.Submit(ctx, s.owner, managerpkg.LifecycleCommand{OperationID: "rm", AddonID: "notes", Action: "uninstall", Generation: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(get("carol")).To(Equal(403))
		Expect(get("alice")).To(Equal(403))
	})

	It("refuses a declared file that is not a regular file of the package", func() {
		// The owner serves bytes from the immutable package only: a symlink
		// planted in the package directory must not reach outside it.
		st, err := s.manager.Status(context.Background(), "notes")
		Expect(err).NotTo(HaveOccurred())
		dir := st.Generations[0].Receipt.Directory
		secret := filepath.Join(GinkgoT().TempDir(), "outside")
		Expect(os.WriteFile(secret, []byte("outside"), 0600)).To(Succeed())
		Expect(os.Chmod(dir, 0700)).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "ui.js"))).To(Succeed())
		Expect(os.Symlink(secret, filepath.Join(dir, "ui.js"))).To(Succeed())
		for _, name := range []string{"owner", "front-a"} {
			w := getAsset(s.frontends[name], base+"ui.js", bearerOf(s.tokens["carol"]))
			Expect(w.Code).NotTo(Equal(200), name)
			Expect(w.Body.String()).NotTo(ContainSubstring("outside"), name)
		}
	})

	It("documents the asset route and its binary response in the Swagger spec", func() {
		spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "swagger", "swagger.json"))
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths map[string]map[string]struct {
				Tags      []string       `json:"tags"`
				Produces  []string       `json:"produces"`
				Responses map[string]any `json:"responses"`
			} `json:"paths"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		op, ok := doc.Paths["/api/addons/{id}/assets/{digest}/{path}"]["get"]
		Expect(ok).To(BeTrue())
		Expect(op.Tags).To(Equal([]string{"Addons"}))
		Expect(op.Produces).To(ContainElements("text/javascript", "text/css"))
		for _, code := range []string{"200", "401", "403", "503"} {
			Expect(op.Responses).To(HaveKey(code))
		}
		text, _ := json.Marshal(op.Responses["200"])
		Expect(string(text)).To(ContainSubstring("immutable"))
		Expect(string(text)).To(ContainSubstring("nosniff"))
	})
})

// forgedHeaders are the forwarding headers a browser could set. None of them
// is authority.
func forgedHeaders(r *http.Request) {
	for _, h := range []string{"X-Forwarded-Actor", "X-Addon-Actor", "X-Forwarded-User", "X-Addon-Peer", "X-Remote-User"} {
		r.Header.Set(h, "alice")
	}
}
