// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/nib/plugin/claudeplugin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	pluginsendpoint "github.com/mudler/LocalAI/core/http/endpoints/plugins"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

const (
	pluginsHMAC = "plugins-routes-secret"
	pluginsSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// pluginsFetcher serves one marketplace and the trees of its plugins.
type pluginsFetcher struct {
	mu    sync.Mutex
	trees map[string]map[string]string
}

func (f *pluginsFetcher) FetchMarketplace(_ context.Context, _, location, _ string) ([]byte, string, error) {
	if location != "owner/team" {
		return nil, "", fmt.Errorf("no marketplace at %s", location)
	}
	entry := func(name string) string {
		return fmt.Sprintf(`{"name":%q,"description":"The %s plugin","source":{"source":"github","repo":"o/%s","sha":%q}}`, name, name, name, pluginsSHA)
	}
	return []byte(`{"name":"team","owner":{"name":"o"},"plugins":[` + entry("fmt") + "," + entry("hook") + `]}`), pluginsSHA, nil
}

func (f *pluginsFetcher) ResolveRef(context.Context, string, string) (string, error) {
	return pluginsSHA, nil
}

func (f *pluginsFetcher) FetchPlugin(_ context.Context, _ fetch.MarketplaceSource, _ *claudeplugin.Marketplace, e *claudeplugin.Entry, sha string) (*fetch.Fetched, error) {
	f.mu.Lock()
	files, ok := f.trees[e.Name]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no plugin %s", e.Name)
	}
	dir := GinkgoT().TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	return &fetch.Fetched{Dir: dir, SHA: sha, Source: e.Source, Done: func() {}}, nil
}

func pluginTree(name string, code bool) map[string]string {
	files := map[string]string{
		".claude-plugin/plugin.json":   `{"name": "` + name + `", "version": "1.0.0", "description": "A fixture"}`,
		"skills/" + name + "/SKILL.md": "---\nname: " + name + "\ndescription: Do it\n---\nBody",
		"commands/run.md":              "---\ndescription: Run it\n---\nRun $ARGUMENTS",
	}
	if code {
		files["hooks/hooks.json"] = `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"}]}]}}`
		files["scripts/check.sh"] = "#!/bin/sh\nexit 0\n"
	}
	return files
}

// inlineJobs runs each job at once.
type inlineJobs struct{}

func (inlineJobs) Enqueue(j plugins.Job) { _ = j.Run(context.Background()) }

// fakeChats keeps the plugin switches of the chats it knows.
type fakeChats struct {
	mu  sync.Mutex
	off map[string][]string
}

func (f *fakeChats) SetPluginsOff(_ context.Context, user, chatID string, off []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := user + "/" + chatID
	if _, ok := f.off[key]; !ok {
		return pluginsendpoint.ErrChatNotFound
	}
	f.off[key] = off
	return nil
}

func newPluginsService() *plugins.Service {
	st, err := store.NewFileStore("")
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	files, err := jsonstore.New("", blobs, jsonstore.Options{Admin: "admin"})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = files.Close() })
	ff := &pluginsFetcher{trees: map[string]map[string]string{"fmt": pluginTree("fmt", false), "hook": pluginTree("hook", true)}}
	svc, err := plugins.New(plugins.Options{
		Store: st, Fetcher: ff, Queue: inlineJobs{}, Root: "/plugins",
		Workspace: content.Workspace{Files: files, Actor: workspace.Actor{Principal: "admin"}},
	})
	Expect(err).NotTo(HaveOccurred())
	_, err = svc.AddMarketplace(context.Background(), plugins.Actor{ID: "seed", Admin: true}, plugins.AddMarketplaceRequest{Source: "owner/team"})
	Expect(err).NotTo(HaveOccurred())
	return svc
}

// pluginsServer is the real middleware chain over the plugin routes.
type pluginsServer struct {
	e     *echo.Echo
	creds map[string]func(*http.Request)
	chats *fakeChats
	svc   *plugins.Service
}

// call sends a request and decodes a JSON answer into out when given.
func (s *pluginsServer) call(method, path, cred, body string, out any, extra ...func(*http.Request)) *httptest.ResponseRecorder {
	GinkgoHelper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if set, ok := s.creds[cred]; ok {
		set(req)
	}
	for _, fn := range extra {
		fn(req)
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 {
		Expect(json.Unmarshal(rec.Body.Bytes(), out)).To(Succeed(), rec.Body.String())
	}
	return rec
}

func errType(rec *httptest.ResponseRecorder) string {
	var e schema.ErrorResponse
	if json.Unmarshal(rec.Body.Bytes(), &e) != nil || e.Error == nil {
		return ""
	}
	return e.Error.Type
}

func confirm(value string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(pluginsendpoint.ConfirmHeader, value) }
}

func browserFetch(r *http.Request) {
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Dest", "empty")
	r.Header.Set("Sec-Fetch-Mode", "cors")
}

func newPluginsServer(svc *plugins.Service, db *gorm.DB, appCfg *config.ApplicationConfig) *pluginsServer {
	e := echo.New()
	e.Use(auth.Middleware(db, appCfg))
	e.Use(auth.RequireRouteFeature(db))
	admin := auth.NoopMiddleware()
	if db != nil {
		admin = auth.RequireAdmin()
	}
	chats := &fakeChats{off: map[string][]string{}}
	d := pluginsendpoint.Deps{
		Service:     func() *plugins.Service { return svc },
		Unavailable: func() string { return "Plugins need a data path." },
		AuthOn:      db != nil || len(appCfg.ApiKeys) > 0,
		Chats:       chats,
	}
	routes.RegisterPluginRoutesFor(e, d, db, admin, true)
	return &pluginsServer{e: e, creds: map[string]func(*http.Request){}, chats: chats, svc: svc}
}

// pluginRoutes lists the plugin routes of a server with a concrete path.
func pluginRoutes(e *echo.Echo) []*echo.Route {
	var out []*echo.Route
	for _, r := range e.Routes() {
		// The group adds catch-all routes for its middleware; they are not
		// routes of the API.
		if r.Path == "/api/plugins" || r.Path == "/api/plugins/*" {
			continue
		}
		if strings.HasPrefix(r.Path, "/api/plugins") || r.Path == "/api/chats/:id/plugins" {
			out = append(out, r)
		}
	}
	return out
}

func concrete(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") || p == "*" {
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}

var _ = Describe("Plugin routes", func() {
	Context("with accounts", func() {
		var s *pluginsServer

		BeforeEach(func() {
			db, err := auth.InitDB(":memory:")
			Expect(err).NotTo(HaveOccurred())
			sqlDB, err := db.DB()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(sqlDB.Close)
			appCfg := &config.ApplicationConfig{}
			appCfg.Auth.APIKeyHMACSecret = pluginsHMAC
			s = newPluginsServer(newPluginsService(), db, appCfg)
			for _, id := range []string{"alice", "carol", "root"} {
				role := auth.RoleUser
				if id == "root" {
					role = auth.RoleAdmin
				}
				Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
				session, err := auth.CreateSession(db, id, pluginsHMAC)
				Expect(err).NotTo(HaveOccurred())
				s.creds[id] = func(r *http.Request) {
					r.AddCookie(&http.Cookie{Name: "session", Value: session})
					browserFetch(r)
				}
				key, _, err := auth.CreateAPIKey(db, id, "test", role, pluginsHMAC, nil)
				Expect(err).NotTo(HaveOccurred())
				s.creds[id+"-key"] = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) }
			}
			// carol had the default-on plugins feature revoked.
			Expect(auth.UpdateUserPermissions(db, "carol", auth.PermissionMap{auth.FeaturePlugins: false})).To(Succeed())
			s.chats.off["alice/c1"] = nil
		})

		It("registers every route of the API", func() {
			Expect(pluginRoutes(s.e)).To(HaveLen(20))
		})

		It("refuses an anonymous caller on every route", func() {
			for _, r := range pluginRoutes(s.e) {
				rec := s.call(r.Method, concrete(r.Path), "", "{}", nil)
				Expect(rec.Code).To(Equal(http.StatusUnauthorized), r.Method+" "+r.Path)
			}
		})

		It("refuses a user whose plugins feature is off on every route, with a session and with a key", func() {
			for _, r := range pluginRoutes(s.e) {
				for _, cred := range []string{"carol", "carol-key"} {
					rec := s.call(r.Method, concrete(r.Path), cred, "{}", nil)
					Expect(rec.Code).To(Equal(http.StatusForbidden), cred+" "+r.Method+" "+r.Path)
				}
			}
		})

		DescribeTable("closes the marketplace administration to a user",
			func(method, path, body string) {
				for _, cred := range []string{"alice", "alice-key"} {
					Expect(s.call(method, path, cred, body, nil).Code).To(Equal(http.StatusForbidden), cred)
				}
			},
			Entry("add", http.MethodPost, "/api/plugins/marketplaces", `{"source":"owner/team"}`),
			Entry("change", http.MethodPut, "/api/plugins/marketplaces/team", `{"enabled":false}`),
			Entry("remove", http.MethodDelete, "/api/plugins/marketplaces/team", ""),
			Entry("refresh", http.MethodPost, "/api/plugins/marketplaces/team/refresh", ""),
		)

		It("lets an administrator manage the marketplaces, with a key too", func() {
			var list pluginsendpoint.MarketplaceList
			Expect(s.call(http.MethodGet, "/api/plugins/marketplaces", "alice", "", &list).Code).To(Equal(http.StatusOK))
			Expect(list.Marketplaces).To(HaveLen(1))
			Expect(list.Marketplaces[0]).To(SatisfyAll(HaveField("ID", "team"), HaveField("Active", true), HaveField("HeadSHA", pluginsSHA)))
			var m pluginsendpoint.Marketplace
			Expect(s.call(http.MethodPost, "/api/plugins/marketplaces/team/refresh", "root-key", "", &m).Code).To(Equal(http.StatusOK))
			Expect(m.RefreshedAt).NotTo(BeNil())
			Expect(s.call(http.MethodPut, "/api/plugins/marketplaces/team", "root", `{"enabled":false}`, &m).Code).To(Equal(http.StatusOK))
			Expect(m.Active).To(BeFalse())
			rec := s.call(http.MethodPost, "/api/plugins/marketplaces", "root", `{"source":"owner/team"}`, nil)
			Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
			rec = s.call(http.MethodPost, "/api/plugins/marketplaces", "root", `{"source":"ssh://host/repo"}`, nil)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(s.call(http.MethodPost, "/api/plugins/marketplaces", "root", `{"source":"owner/team","bogus":1}`, nil).Code).To(Equal(http.StatusBadRequest))
			Expect(s.call(http.MethodDelete, "/api/plugins/marketplaces/team", "root", "", nil).Code).To(Equal(http.StatusNoContent))
			Expect(s.call(http.MethodDelete, "/api/plugins/marketplaces/team", "root", "", nil).Code).To(Equal(http.StatusNotFound))
		})

		It("lets a user browse the catalog and read one entry", func() {
			var page plugins.CatalogPage
			Expect(s.call(http.MethodGet, "/api/plugins/catalog?q=hook", "alice-key", "", &page).Code).To(Equal(http.StatusOK))
			Expect(page.Items).To(HaveLen(1))
			Expect(page.Items[0].PluginID).To(Equal("hook@team"))
			Expect(s.call(http.MethodGet, "/api/plugins/catalog?runs_code=maybe", "alice", "", nil).Code).To(Equal(http.StatusBadRequest))
			Expect(s.call(http.MethodGet, "/api/plugins/catalog?limit=x", "alice", "", nil).Code).To(Equal(http.StatusBadRequest))
			var it plugins.CatalogItem
			Expect(s.call(http.MethodGet, "/api/plugins/catalog/team/fmt", "alice", "", &it).Code).To(Equal(http.StatusOK))
			Expect(it.Description).To(Equal("The fmt plugin"))
			Expect(s.call(http.MethodGet, "/api/plugins/catalog/team/nope", "alice", "", nil).Code).To(Equal(http.StatusNotFound))
		})

		inspect := func(cred, plugin string) plugins.Preview {
			GinkgoHelper()
			var p plugins.Preview
			rec := s.call(http.MethodPost, "/api/plugins/inspect", cred, `{"plugin":"`+plugin+`"}`, &p)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			return p
		}
		install := func(cred, token, scope string, extra ...func(*http.Request)) *httptest.ResponseRecorder {
			return s.call(http.MethodPost, "/api/plugins/installs", cred, `{"token":"`+token+`","scope":"`+scope+`"}`, nil, extra...)
		}

		It("lets a user install a text-only plugin for themselves, through an API key too", func() {
			p := inspect("alice-key", "fmt@team")
			Expect(p.RunsCode).To(BeFalse())
			var job pluginsendpoint.JobAccepted
			rec := s.call(http.MethodPost, "/api/plugins/installs", "alice-key", `{"token":"`+p.Token+`"}`, &job)
			Expect(rec.Code).To(Equal(http.StatusAccepted), rec.Body.String())
			Expect(job.JobID).NotTo(BeEmpty())
			Expect(job.PluginID).To(Equal("fmt@team"))
			Expect(errType(install("alice-key", p.Token, "user"))).To(Equal(pluginsendpoint.ErrTypeInvalidToken), "a token works once")

			var list pluginsendpoint.InstallList
			Expect(s.call(http.MethodGet, "/api/plugins/installs", "alice", "", &list).Code).To(Equal(http.StatusOK))
			Expect(list.Installs).To(HaveLen(1))
			Expect(list.Installs[0]).To(SatisfyAll(HaveField("Scope", store.ScopeUser), HaveField("Enabled", true)))
			Expect(s.call(http.MethodGet, "/api/plugins/installs?all=true", "alice", "", nil).Code).To(Equal(http.StatusForbidden))
			Expect(s.call(http.MethodGet, "/api/plugins/installs?all=true", "root", "", &list).Code).To(Equal(http.StatusOK))
			Expect(list.Installs).To(HaveLen(1))
		})

		It("answers 429 rate_limited when a user inspects too often", func() {
			for i := 0; i < plugins.InspectBurst; i++ {
				inspect("alice", "fmt@team")
			}
			rec := s.call(http.MethodPost, "/api/plugins/inspect", "alice", `{"plugin":"fmt@team"}`, nil)
			Expect(rec.Code).To(Equal(http.StatusTooManyRequests), rec.Body.String())
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeRateLimited))
		})

		It("refuses a user the install of a plugin that runs code, with the reason", func() {
			p := inspect("alice", "hook@team")
			Expect(p.RunsCode).To(BeTrue())
			rec := install("alice", p.Token, "user", confirm("hook@team"))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNeedsAdmin))
			rec = install("alice", p.Token, "instance", confirm("hook@team"))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeForbidden))
		})

		It("installs a plugin that runs code only from the administrator's browser session, for everyone", func() {
			p := inspect("root-key", "hook@team")
			rec := install("root-key", p.Token, "instance", confirm("hook@team"))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNotHumanSurface), "an API key is never the browser")
			rec = install("root", p.Token, "instance")
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNotHumanSurface), "a session without the confirmation")
			rec = install("root", p.Token, "instance", confirm("fmt@team"))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNotHumanSurface), "a confirmation of another plugin")
			rec = install("root", p.Token, "instance", confirm("hook@team"), func(r *http.Request) { r.Header.Set("X-LocalAI-Tool-Call", "1") })
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNotHumanSurface), "a tool call")
			// Without Sec-Fetch-Site (plain HTTP on a LAN address) the request
			// must name this host, and a destination or mode that is present
			// must still be the one of a fetch.
			noSite := func(dest, mode string) func(*http.Request) {
				return func(r *http.Request) {
					r.Header.Del("Sec-Fetch-Site")
					r.Header.Del("Sec-Fetch-Dest")
					r.Header.Del("Sec-Fetch-Mode")
					r.Header.Set("Origin", "http://"+r.Host)
					if dest != "" {
						r.Header.Set("Sec-Fetch-Dest", dest)
					}
					if mode != "" {
						r.Header.Set("Sec-Fetch-Mode", mode)
					}
				}
			}
			rec = install("root", p.Token, "instance", confirm("hook@team"), noSite("document", ""))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNotHumanSurface), "a stray Sec-Fetch-Dest")
			rec = install("root", p.Token, "instance", confirm("hook@team"), noSite("", "navigate"))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNotHumanSurface), "a stray Sec-Fetch-Mode")
			rec = install("root", p.Token, "user", confirm("hook@team"))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeCodeForEveryone))
			rec = install("root", p.Token, "instance", confirm("hook@team"))
			Expect(rec.Code).To(Equal(http.StatusAccepted), rec.Body.String())

			q := inspect("alice", "hook@team")
			Expect(errType(install("alice", q.Token, "user"))).To(Equal(pluginsendpoint.ErrTypeNeedsAdmin))
		})

		It("answers 409 naming the install for everyone when a user installs the same plugin", func() {
			p := inspect("root", "fmt@team")
			Expect(install("root", p.Token, "instance").Code).To(Equal(http.StatusAccepted))
			q := inspect("alice", "fmt@team")
			rec := install("alice", q.Token, "user")
			Expect(rec.Code).To(Equal(http.StatusConflict))
			var e schema.ErrorResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &e)).To(Succeed())
			Expect(e.Error.Type).To(Equal(pluginsendpoint.ErrTypeInstanceInstall))
			Expect(e.Error.Param).NotTo(BeNil())
			r := inspect("root", "fmt@team")
			Expect(errType(install("root", r.Token, "instance"))).To(Equal(pluginsendpoint.ErrTypeConflict))
		})

		It("serves the files of a preview and of an install to the caller only", func() {
			p := inspect("alice", "fmt@team")
			rec := s.call(http.MethodGet, "/api/plugins/inspect/"+p.Token+"/files/commands/run.md", "alice", "", nil)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(ContainSubstring("Run $ARGUMENTS"))
			Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/plain"))
			Expect(rec.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
			Expect(s.call(http.MethodGet, "/api/plugins/inspect/"+p.Token+"/files/commands/run.md", "root", "", nil).Code).To(Equal(http.StatusBadRequest))
			Expect(s.call(http.MethodGet, "/api/plugins/inspect/"+p.Token+"/files/nope.md", "alice", "", nil).Code).To(Equal(http.StatusNotFound))

			Expect(install("alice", p.Token, "user").Code).To(Equal(http.StatusAccepted))
			var list pluginsendpoint.InstallList
			s.call(http.MethodGet, "/api/plugins/installs", "alice", "", &list)
			id := list.Installs[0].ID
			var d plugins.InstallDetail
			Expect(s.call(http.MethodGet, "/api/plugins/installs/"+id, "alice", "", &d).Code).To(Equal(http.StatusOK))
			Expect(d.Model.Commands).To(HaveLen(1))
			Expect(s.call(http.MethodGet, "/api/plugins/installs/"+id+"/files/skills/fmt/SKILL.md", "alice", "", nil).Code).To(Equal(http.StatusOK))
			Expect(s.call(http.MethodGet, "/api/plugins/installs/"+id, "carol", "", nil).Code).To(Equal(http.StatusForbidden), "feature off")
			Expect(s.call(http.MethodGet, "/api/plugins/installs/"+id, "root", "", nil).Code).To(Equal(http.StatusOK))
		})

		It("changes, updates, rolls back and removes an install with the rights of spec 9.2", func() {
			p := inspect("root", "fmt@team")
			Expect(install("root", p.Token, "instance").Code).To(Equal(http.StatusAccepted))
			var list pluginsendpoint.InstallList
			s.call(http.MethodGet, "/api/plugins/installs", "alice", "", &list)
			Expect(list.Installs).To(HaveLen(1))
			id := list.Installs[0].ID
			Expect(list.Installs[0].Enabled).To(BeFalse(), "an install for everyone starts off for a user")

			var me plugins.MyState
			Expect(s.call(http.MethodPut, "/api/plugins/installs/"+id+"/me", "alice", `{"enabled":true}`, &me).Code).To(Equal(http.StatusOK))
			Expect(me.Enabled).To(BeTrue())
			Expect(s.call(http.MethodGet, "/api/plugins/installs/"+id+"/me", "alice-key", "", &me).Code).To(Equal(http.StatusOK))
			Expect(me.Enabled).To(BeTrue())
			Expect(s.call(http.MethodPut, "/api/plugins/installs/"+id+"/me", "alice", `{"secrets":{"k":"v"}}`, nil).Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(s.call(http.MethodPut, "/api/plugins/installs/"+id+"/me", "alice", `{"enabled":"yes"}`, nil).Code).To(Equal(http.StatusBadRequest))

			Expect(s.call(http.MethodPost, "/api/plugins/installs/"+id+"/update/preview", "alice", "", nil).Code).To(Equal(http.StatusForbidden))
			rec := s.call(http.MethodPost, "/api/plugins/installs/"+id+"/update/preview", "root", "", nil)
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNoUpdate))
			Expect(s.call(http.MethodPost, "/api/plugins/installs/"+id+"/update", "root", `{"token":"nope"}`, nil).Code).To(Equal(http.StatusBadRequest))
			Expect(s.call(http.MethodPost, "/api/plugins/installs/"+id+"/rollback", "root", "", nil).Code).To(Equal(http.StatusBadRequest), "no previous version")

			Expect(s.call(http.MethodDelete, "/api/plugins/installs/"+id, "alice", "", nil).Code).To(Equal(http.StatusForbidden))
			Expect(s.call(http.MethodDelete, "/api/plugins/installs/"+id, "root-key", "", nil).Code).To(Equal(http.StatusNoContent))
			Expect(s.call(http.MethodGet, "/api/plugins/installs/"+id, "root", "", nil).Code).To(Equal(http.StatusNotFound))
		})

		It("lets the owner remove a personal install, and hides it from other users", func() {
			p := inspect("alice", "fmt@team")
			Expect(install("alice", p.Token, "user").Code).To(Equal(http.StatusAccepted))
			var list pluginsendpoint.InstallList
			s.call(http.MethodGet, "/api/plugins/installs", "alice", "", &list)
			id := list.Installs[0].ID
			Expect(s.call(http.MethodGet, "/api/plugins/installs/"+id+"/me", "root", "", nil).Code).To(Equal(http.StatusNotFound))
			Expect(s.call(http.MethodDelete, "/api/plugins/installs/"+id, "alice-key", "", nil).Code).To(Equal(http.StatusNoContent))
		})

		It("sets the plugin switches of a chat", func() {
			var out pluginsendpoint.ChatPluginsResponse
			Expect(s.call(http.MethodPut, "/api/chats/c1/plugins", "alice", `{"off":["i1","i1","i2"]}`, &out).Code).To(Equal(http.StatusOK))
			Expect(out.Off).To(Equal([]string{"i1", "i2"}))
			Expect(s.chats.off["alice/c1"]).To(Equal([]string{"i1", "i2"}))
			Expect(s.call(http.MethodPut, "/api/chats/c2/plugins", "alice", `{"off":[]}`, nil).Code).To(Equal(http.StatusNotFound))
			Expect(s.call(http.MethodPut, "/api/chats/c1/plugins", "alice", `{"off":[""]}`, nil).Code).To(Equal(http.StatusBadRequest))
			many := make([]string, 201)
			for i := range many {
				many[i] = fmt.Sprintf("%q", fmt.Sprintf("i%d", i))
			}
			Expect(s.call(http.MethodPut, "/api/chats/c1/plugins", "alice", `{"off":[`+strings.Join(many, ",")+`]}`, nil).Code).To(Equal(http.StatusBadRequest))
		})
	})

	Context("without authentication", func() {
		var s *pluginsServer

		BeforeEach(func() {
			s = newPluginsServer(newPluginsService(), nil, &config.ApplicationConfig{})
			s.creds["browser"] = browserFetch
		})

		It("treats the local owner as an administrator who confirms code from the browser", func() {
			Expect(s.call(http.MethodPost, "/api/plugins/marketplaces/team/refresh", "", "", nil).Code).To(Equal(http.StatusOK))
			var p plugins.Preview
			Expect(s.call(http.MethodPost, "/api/plugins/inspect", "", `{"plugin":"hook@team"}`, &p).Code).To(Equal(http.StatusOK))
			body := `{"token":"` + p.Token + `","scope":"instance"}`
			rec := s.call(http.MethodPost, "/api/plugins/installs", "", body, nil, confirm("hook@team"))
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeNotHumanSurface), "no same-origin fetch")
			rec = s.call(http.MethodPost, "/api/plugins/installs", "browser", body, nil, confirm("hook@team"))
			Expect(rec.Code).To(Equal(http.StatusAccepted), rec.Body.String())
		})

		It("answers the chat switch with 501 while the chats cannot keep it", func() {
			d := pluginsendpoint.Deps{Service: func() *plugins.Service { return s.svc }}
			e := echo.New()
			routes.RegisterPluginRoutesFor(e, d, nil, auth.NoopMiddleware(), true)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/chats/c1/plugins", strings.NewReader(`{"off":[]}`))
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	It("answers 503 with the reason on every route when the service is off", func() {
		s := newPluginsServer(nil, nil, &config.ApplicationConfig{})
		for _, r := range pluginRoutes(s.e) {
			if r.Path == "/api/chats/:id/plugins" {
				continue
			}
			rec := s.call(r.Method, concrete(r.Path), "", "{}", nil)
			Expect(rec.Code).To(Equal(http.StatusServiceUnavailable), r.Method+" "+r.Path)
			Expect(errType(rec)).To(Equal(pluginsendpoint.ErrTypeUnavailable))
			Expect(rec.Body.String()).To(ContainSubstring("data path"))
		}
	})
})
