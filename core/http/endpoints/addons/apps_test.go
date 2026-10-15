// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// appsStack is the owner stack with a real lifecycle manager that has two
// installed addons: "notes" (UI, icon, main page, tools; enabled) and
// "headless" (tools only; installed but disabled).
type appsStack struct {
	*ownerStack
	manager *managerpkg.Manager
	icon    []byte
	root    string
	// frontends are the owner's own frontend and two forwarding frontends.
	frontends map[string]http.Handler
}

func newAppsStack() *appsStack {
	s := &appsStack{ownerStack: newOwnerStack(auth.AddonModeNamedUser, true)}
	for _, u := range []struct {
		id      string
		granted bool
	}{{"carol", true}, {"dave", false}} {
		Expect(s.db.Create(&auth.User{ID: u.id, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		token, _, err := auth.CreateAPIKey(s.db, u.id, "agent", auth.RoleUser, "secret", nil)
		Expect(err).NotTo(HaveOccurred())
		s.tokens[u.id] = token
		cookie, err := auth.CreateSession(s.db, u.id, "secret")
		Expect(err).NotTo(HaveOccurred())
		s.cookies[u.id] = cookie
		if u.granted {
			Expect(s.db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: u.id, Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error).To(Succeed())
		}
	}
	s.icon = append([]byte("\x89PNG\r\n\x1a\n"), []byte("icon-bytes")...)
	ctx := context.Background()
	archives := map[string][]byte{}
	for id, files := range map[string]map[string][]byte{
		"notes": {
			"manifest.json": []byte(`{"schemaVersion":1,"id":"notes","version":"1.0.0","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":1,"max":1},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"launchMode":"always","capabilities":{"service":true,"tools":["add","list"],"ui":{"entry":"ui.js","css":["ui/style.css"],"sdk":{"min":1,"max":1},"pages":["home"],"navigation":["home"],"chatActions":["summarize"],"mainPage":"home","icon":"ui/icon.png","title":{"en":"Notes","it":"Appunti"},"description":"Keep notes."}}}`),
			"ui.js":         []byte("export default {};"),
			"ui/style.css":  []byte(".notes{color:red}"),
			"ui/icon.png":   s.icon,
			"secret.txt":    []byte("owner only"),
		},
		"headless": {
			"manifest.json": []byte(`{"schemaVersion":1,"id":"headless","version":"1.0.0","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":1,"max":1},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true,"tools":["query"]}}`),
		},
	} {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		files["bin/sample"] = []byte("binary")
		for name, data := range files {
			Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
			_, err := tw.Write(data)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(tw.Close()).To(Succeed())
		archives[id] = b.Bytes()
	}
	artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archives[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".tar")])
	}))
	DeferCleanup(artifact.Close)
	releases := []catalog.Release{}
	for _, id := range []string{"notes", "headless"} {
		releases = append(releases, catalog.Release{AddonID: id, Name: id, Version: "1.0.0", Artifact: artifact.URL + "/" + id + ".tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(archives[id])), OS: "linux", Arch: "amd64"})
	}
	library := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = catalog.WriteIndex(w, releases) }))
	DeferCleanup(library.Close)
	root := GinkgoT().TempDir()
	s.root = root
	admin := func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, s.db, a, true) }
	sources, err := catalog.NewSources(filepath.Join(root, "sources.json"), admin, appsResolver)
	Expect(err).NotTo(HaveOccurred())
	Expect(sources.Save(ctx, s.owner, catalog.Source{ID: "test", Name: "Test", URL: library.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	_, err = sources.Refresh(ctx, "test")
	Expect(err).NotTo(HaveOccurred())
	c := catalog.Catalog{Sources: sources, TargetOS: "linux", TargetArch: "amd64"}
	cfg := managerpkg.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: filepath.Join(root, "owner")}
	installer := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: c, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
	DeferCleanup(func() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				_ = os.Chmod(p, 0700)
			}
			return nil
		})
	})
	s.manager, err = managerpkg.NewManager(ctx, cfg, c, installer, admin)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(s.manager.Close()).To(Succeed()) })
	for _, id := range []string{"notes", "headless"} {
		_, err = s.manager.Submit(ctx, s.owner, managerpkg.LifecycleCommand{OperationID: "install-" + id, AddonID: id, Action: "install", SourceID: "test", Version: "1.0.0"})
		Expect(err).NotTo(HaveOccurred())
	}
	_, err = s.manager.Submit(ctx, s.owner, managerpkg.LifecycleCommand{OperationID: "enable-notes", AddonID: "notes", Action: "enable", Generation: 1})
	Expect(err).NotTo(HaveOccurred())
	s.gateway.Manager = s.manager
	a, b := s.forwardingFrontends(s.gateway)
	s.frontends = map[string]http.Handler{"owner": s.frontend(s.gateway), "front-a": a, "front-b": b}
	return s
}

// each runs check against the owner's frontend and both forwarding frontends.
func (s *appsStack) each(check func(name string, e http.Handler)) {
	for _, name := range []string{"owner", "front-a", "front-b"} {
		check(name, s.frontends[name])
	}
}

func appsOf(r reply) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := r.body["apps"].([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		id, _ := m["id"].(string)
		out[id] = m
	}
	return out
}

var _ = Describe("apps routes", Label("apps"), func() {
	var s *appsStack
	BeforeEach(func() { s = newAppsStack() })

	It("lists the enabled installed apps for a reader and the disabled ones too for administrators, the same from every frontend", func() {
		s.each(func(name string, e http.Handler) {
			reader := send(e, "GET", "/api/addons/apps", "", bearerOf(s.tokens["carol"]))
			Expect(reader.code).To(Equal(200), name)
			got := appsOf(reader)
			Expect(got).To(HaveLen(1), name)
			notes := got["notes"]
			Expect(notes).To(HaveKeyWithValue("title", "Notes"))
			Expect(notes).To(HaveKeyWithValue("titles", map[string]any{"en": "Notes", "it": "Appunti"}))
			Expect(notes).To(HaveKeyWithValue("description", "Keep notes."))
			Expect(notes).To(HaveKeyWithValue("version", "1.0.0"))
			Expect(notes).To(HaveKeyWithValue("generation", BeNumerically("==", 1)))
			Expect(notes).To(HaveKeyWithValue("enabled", true))
			Expect(notes).To(HaveKeyWithValue("state", "stopped"), "no process is attached to this owner")
			Expect(notes).To(HaveKeyWithValue("mainPage", "/app/addons/notes/home"))
			Expect(notes).To(HaveKeyWithValue("icon", "/api/addons/apps/notes/icon?generation=1"))
			Expect(notes).To(HaveKeyWithValue("hasUI", true))
			Expect(notes).To(HaveKeyWithValue("launchMode", "always"))
			Expect(notes).To(HaveKeyWithValue("tools", []any{"add", "list"}))
			Expect(notes).NotTo(HaveKey("admin"), "a reader gets no controls")
			Expect(reader.body).NotTo(HaveKey("error"))

			admin := send(e, "GET", "/api/addons/apps", "", bearerOf(s.tokens["alice"]))
			Expect(admin.code).To(Equal(200), name)
			got = appsOf(admin)
			Expect(got).To(HaveLen(2), name)
			Expect(got["notes"]["admin"]).To(Equal(map[string]any{"canStart": false, "canStop": true, "managementUrl": "/app/addons?manage=notes"}))
			headless := got["headless"]
			Expect(headless["admin"]).To(Equal(map[string]any{"canStart": true, "canStop": false, "managementUrl": "/app/addons?manage=headless"}))
			Expect(headless).To(HaveKeyWithValue("hasUI", false))
			Expect(headless).NotTo(HaveKey("mainPage"), "a headless addon has no Open action")
			Expect(headless).NotTo(HaveKey("icon"))
			Expect(headless).To(HaveKeyWithValue("tools", []any{"query"}))

			// Nothing of the owner's storage is emitted.
			raw := send(e, "GET", "/api/addons/apps", "", bearerOf(s.tokens["alice"]))
			b, _ := json.Marshal(raw.body)
			for _, secret := range []string{"packages", "package-", "receipt", "sha256:", "127.0.0.1", "endpoint", s.root} {
				Expect(string(b)).NotTo(ContainSubstring(secret), secret)
			}

			Expect(send(e, "GET", "/api/addons/apps", "", nil).code).To(Equal(401), name)
			Expect(send(e, "GET", "/api/addons/apps", "", bearerOf(s.tokens["dave"])).code).To(Equal(403), name+": no addons feature")
			// A forged forwarding header changes nothing.
			forged := send(e, "GET", "/api/addons/apps", "", func(r *http.Request) {
				bearerOf(s.tokens["carol"])(r)
				r.Header.Set("X-Forwarded-Actor", "alice")
				r.Header.Set("X-Addon-Actor", "alice")
			})
			Expect(appsOf(forged)).To(HaveLen(1), name)
		})
	})

	It("lets only administrators start and stop, through the lifecycle manager, and denies a stale generation", func() {
		ctx := context.Background()
		desired := func(id string) bool {
			st, err := s.manager.Status(ctx, id)
			Expect(err).NotTo(HaveOccurred())
			return st.DesiredEnabled
		}
		body := func(op string, generation int) string {
			return fmt.Sprintf(`{"operationId":%q,"generation":%d}`, op, generation)
		}
		s.each(func(name string, e http.Handler) {
			// Non-administrators are denied however the request is formed.
			for _, who := range []string{"carol", "dave"} {
				for _, payload := range []string{body("op-"+name+who, 1), `{}`, `not json`} {
					r := send(e, "POST", "/api/addons/apps/headless/start", payload, bearerOf(s.tokens[who]))
					Expect(r.code).To(Equal(403), name+who)
				}
			}
			Expect(desired("headless")).To(BeFalse())
			Expect(send(e, "POST", "/api/addons/apps/headless/start", body("anon", 1), nil).code).To(Equal(401))
			// A browser session without same-origin metadata is refused before the owner.
			cookie := send(e, "POST", "/api/addons/apps/headless/start", body("cookie-"+name, 1), func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session", Value: s.cookies["alice"]})
			})
			Expect(cookie.code).To(BeElementOf(400, 403))
			Expect(desired("headless")).To(BeFalse())

			// A stale generation is denied and records nothing.
			stale := send(e, "POST", "/api/addons/apps/headless/start", body("stale-"+name, 9), bearerOf(s.tokens["alice"]))
			Expect(stale.code).To(Equal(403), name)
			_, err := s.manager.OperationStatus(ctx, s.owner, "stale-"+name)
			Expect(err).To(MatchError(managerpkg.ErrOperationNotFound))
			// The request must say which generation the caller saw.
			Expect(send(e, "POST", "/api/addons/apps/headless/start", `{"operationId":"nogen"}`, bearerOf(s.tokens["alice"])).code).To(Equal(400))
			Expect(send(e, "POST", "/api/addons/apps/headless/start", `{"generation":1}`, bearerOf(s.tokens["alice"])).code).To(Equal(400))
			Expect(send(e, "POST", "/api/addons/apps/headless/start", `{"operationId":"x","generation":1,"action":"uninstall"}`, bearerOf(s.tokens["alice"])).code).To(Equal(400), "no room for another action")
			Expect(send(e, "POST", "/api/addons/apps/nothing/start", body("unknown-"+name, 1), bearerOf(s.tokens["alice"])).code).To(Equal(403))
		})
		Expect(desired("headless")).To(BeFalse())

		// Start, then stop, each through a different frontend.
		start := send(s.frontends["front-a"], "POST", "/api/addons/apps/headless/start", body("start-1", 1), bearerOf(s.tokens["alice"]))
		Expect(start.code).To(Equal(200))
		Expect(start.body["payload"]).To(HaveKeyWithValue("Phase", "completed"))
		Expect(desired("headless")).To(BeTrue())
		again := send(s.frontends["front-b"], "POST", "/api/addons/apps/headless/start", body("start-1", 1), bearerOf(s.tokens["alice"]))
		Expect(again.code).To(Equal(200), "the same operation replays instead of running twice")
		reader := appsOf(send(s.frontends["owner"], "GET", "/api/addons/apps", "", bearerOf(s.tokens["carol"])))
		Expect(reader).To(HaveKey("headless"))
		stop := send(s.frontends["front-b"], "POST", "/api/addons/apps/notes/stop", body("stop-1", 1), bearerOf(s.tokens["alice"]))
		Expect(stop.code).To(Equal(200))
		Expect(desired("notes")).To(BeFalse())
		reader = appsOf(send(s.frontends["front-a"], "GET", "/api/addons/apps", "", bearerOf(s.tokens["carol"])))
		Expect(reader).NotTo(HaveKey("notes"), "a stopped app disappears for readers")
		admin := appsOf(send(s.frontends["owner"], "GET", "/api/addons/apps", "", bearerOf(s.tokens["alice"])))
		Expect(admin["notes"]["state"]).To(Equal("stopped"))
		Expect(admin["notes"]["admin"]).To(HaveKeyWithValue("canStart", true))
		// Data is retained: starting it again needs the new generation view.
		restart := send(s.frontends["owner"], "POST", "/api/addons/apps/notes/start", body("start-2", 1), bearerOf(s.tokens["alice"]))
		Expect(restart.code).To(Equal(200))
		Expect(desired("notes")).To(BeTrue())
	})
})

var _ = Describe("apps routes outcomes and documentation", Label("apps"), func() {
	It("serves the verified icon through the host route and denies it identically otherwise", func() {
		s := newAppsStack()
		get := func(e http.Handler, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
			r := httptest.NewRequest("GET", path, nil)
			mutate(r)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w
		}
		s.each(func(name string, e http.Handler) {
			ok := get(e, "/api/addons/apps/notes/icon?generation=1", bearerOf(s.tokens["carol"]))
			Expect(ok.Code).To(Equal(200), name)
			Expect(ok.Body.Bytes()).To(Equal(s.icon), name)
			Expect(ok.Header().Get("Content-Type")).To(Equal("image/png"))
			Expect(ok.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
			Expect(ok.Header().Get("Cache-Control")).To(ContainSubstring("no-store"))
			Expect(ok.Header().Get("Content-Disposition")).To(BeEmpty())

			// Stale generation, unknown, iconless and traversing ids are one denial.
			var denial []byte
			for _, path := range []string{"/api/addons/apps/notes/icon?generation=2", "/api/addons/apps/headless/icon?generation=1", "/api/addons/apps/missing/icon?generation=1", "/api/addons/apps/..%2Fnotes/icon?generation=1"} {
				w := get(e, path, bearerOf(s.tokens["alice"]))
				Expect(w.Code).To(Equal(403), name+" "+path)
				if denial == nil {
					denial = w.Body.Bytes()
				}
				Expect(w.Body.Bytes()).To(Equal(denial), name+" "+path)
			}
			Expect(get(e, "/api/addons/apps/notes/icon?generation=x", bearerOf(s.tokens["carol"])).Code).To(Equal(400))
			Expect(get(e, "/api/addons/apps/notes/icon", bearerOf(s.tokens["carol"])).Code).To(Equal(400))
			Expect(get(e, "/api/addons/apps/notes/icon?generation=1", func(*http.Request) {}).Code).To(Equal(401))
			Expect(get(e, "/api/addons/apps/notes/icon?generation=1", bearerOf(s.tokens["dave"])).Code).To(Equal(403))
		})
		// A stopped app's icon is not served to a reader but is to an administrator.
		_, err := s.manager.Submit(context.Background(), s.owner, managerpkg.LifecycleCommand{OperationID: "off", AddonID: "notes", Action: "disable", Generation: 1})
		Expect(err).NotTo(HaveOccurred())
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/addons/apps/notes/icon?generation=1", nil)
		bearerOf(s.tokens["carol"])(r)
		s.frontends["front-a"].ServeHTTP(w, r)
		Expect(w.Code).To(Equal(403))
		w = httptest.NewRecorder()
		r = httptest.NewRequest("GET", "/api/addons/apps/notes/icon?generation=1", nil)
		bearerOf(s.tokens["alice"])(r)
		s.frontends["front-a"].ServeHTTP(w, r)
		Expect(w.Code).To(Equal(200))
	})

	It("reports a lost start as an unknown outcome with its original operation and a lost list as unavailable", func() {
		s := newAppsStack()
		certs, roots := certificates()
		pins := map[string][32]byte{}
		keys := map[string]ed25519.PublicKey{}
		private := map[string]ed25519.PrivateKey{}
		for _, id := range []string{"owner", "front-a"} {
			pins[id] = sha256.Sum256(certs[id].Certificate[0])
			pub, priv, e := ed25519.GenerateKey(rand.Reader)
			Expect(e).NotTo(HaveOccurred())
			keys[id], private[id] = pub, priv
		}
		owner := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/addons/audience" {
				_ = json.NewEncoder(w).Encode(map[string]string{"audience": strings.Repeat("a", 64)})
				return
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		carrier := func(id string) *gateway.Carrier {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + owner.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			return c
		}
		owner.TLS = carrier("owner").ServerTLS()
		owner.StartTLS()
		DeferCleanup(owner.Close)
		front := s.frontend(&gateway.Gateway{Forward: carrier("front-a"), Revalidate: auth.AddonRevalidate(s.db, auth.AddonModeNamedUser)})
		r := send(front, "POST", "/api/addons/apps/headless/start", `{"operationId":"lost-start","generation":1}`, bearerOf(s.tokens["alice"]))
		Expect(r.code).To(Equal(503))
		Expect(r.errType()).To(Equal("outcome_unknown"))
		Expect(r.errOperation()).To(Equal("lost-start"))
		r = send(front, "GET", "/api/addons/apps", "", bearerOf(s.tokens["carol"]))
		Expect(r.code).To(Equal(503))
		Expect(r.errType()).To(Equal("addon_error"))
		Expect(r.errOperation()).To(BeEmpty())
	})

	It("documents the apps routes and every field of the app view in the Swagger spec", func() {
		spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "swagger", "swagger.json"))
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths       map[string]map[string]any `json:"paths"`
			Definitions map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"definitions"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		for path, method := range map[string]string{
			"/api/addons/apps":            "get",
			"/api/addons/apps/{id}/icon":  "get",
			"/api/addons/apps/{id}/start": "post",
			"/api/addons/apps/{id}/stop":  "post",
		} {
			Expect(doc.Paths).To(HaveKey(path))
			Expect(doc.Paths[path]).To(HaveKey(method), path)
			text, _ := json.Marshal(doc.Paths[path])
			Expect(string(text)).To(ContainSubstring("Addons"), path)
		}
		start, _ := json.Marshal(doc.Paths["/api/addons/apps/{id}/start"])
		for _, text := range []string{"generation", "403", "503", "outcome"} {
			Expect(string(start)).To(ContainSubstring(text))
		}
		// Every field the views emit is documented, and nothing else is.
		emitted := func(v any) map[string]any {
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			var m map[string]any
			Expect(json.Unmarshal(b, &m)).To(Succeed())
			return m
		}
		full := apps.App{ID: "a", Name: "a", Title: "t", Titles: map[string]string{"en": "t"}, Description: "d", Descriptions: map[string]string{"en": "d"}, Icon: "i", Version: "1", Generation: 1, Enabled: true, State: "running", Update: "update-pending", MainPage: "/m", HasUI: true, LaunchMode: "always", Tools: []string{"x"}, ToolCount: 1, ToolDefinitions: []contract.ToolDefinition{{Name: "x", Description: "d", Kind: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}, UI: &apps.UIView{Entry: "/e", CSS: []string{"/c"}, SDK: contract.VersionRange{Min: 1, Max: 1}, Actions: []apps.ChatActionView{{ID: "a", Label: "A", Labels: map[string]string{"en": "A"}, Icon: "star", PackagedIcon: true, Placement: "composer", Tool: "t", Component: "C"}}, Slots: []apps.SlotView{{ID: "s", Slot: "app.nav", Component: "C"}}, Renderers: []contract.Renderer{{Kind: "approval-card", Tool: "t", Component: "C"}}, Pages: []string{"p"}, Navigation: []string{"p"}, ChatActions: []string{"a"}}, Admin: &apps.AdminView{CanStart: true, CanStop: true, ManagementURL: "/x"}}
		for name, v := range map[string]any{"apps.App": full, "apps.AdminView": *full.Admin, "apps.UIView": *full.UI, "apps.ChatActionView": full.UI.Actions[0], "apps.SlotView": full.UI.Slots[0], "contract.Renderer": full.UI.Renderers[0], "contract.VersionRange": full.UI.SDK, "contract.ToolDefinition": full.ToolDefinitions[0], "apps.List": apps.List{Apps: []apps.App{full}}} {
			def, ok := doc.Definitions[name]
			Expect(ok).To(BeTrue(), name)
			shape := emitted(v)
			for field := range shape {
				Expect(def.Properties).To(HaveKey(field), name+"."+field)
			}
			for field := range def.Properties {
				Expect(shape).To(HaveKey(field), name+" documents "+field+" that is never emitted")
			}
		}
		Expect(doc.Definitions["apps.App"].Properties).NotTo(HaveKey("directory"))
		Expect(doc.Definitions["apps.StartRequest"].Properties).To(HaveKey("generation"))
		Expect(doc.Definitions["apps.StartRequest"].Properties).To(HaveKey("operationId"))
	})
})
