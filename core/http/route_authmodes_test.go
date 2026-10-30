//go:build auth

package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	authModesHMACSecret = "test-secret-for-auth-modes"
	authModesLegacyKey  = "legacy-key"
)

type authModesCredential struct {
	name   string
	header func(req *http.Request)
}

type authModesServer struct {
	e     *echo.Echo
	creds map[string]authModesCredential
}

// routesUnderTest lists every chats and add-on route the server registers.
func (s *authModesServer) routesUnderTest() []*echo.Route {
	seen := map[string]bool{}
	found := []*echo.Route{}
	for _, r := range s.e.Routes() {
		if !strings.HasPrefix(r.Path, "/api/chats") && !strings.HasPrefix(r.Path, "/api/addons") {
			continue
		}
		key := r.Method + " " + r.Path
		if seen[key] || r.Method == http.MethodOptions || r.Method == http.MethodHead {
			continue
		}
		seen[key] = true
		found = append(found, r)
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].Path+" "+found[i].Method < found[j].Path+" "+found[j].Method
	})
	return found
}

func authModesConcretize(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") || p == "*" {
			parts[i] = "test"
		}
	}
	return strings.Join(parts, "/")
}

// status sends the request of one route with the credential and returns the
// status code. Streaming routes are cancelled right after the headers.
func (s *authModesServer) status(r *echo.Route, cred string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(r.Method, authModesConcretize(r.Path), strings.NewReader("{}")).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if c, ok := s.creds[cred]; ok && c.header != nil {
		c.header(req)
	}
	rec := httptest.NewRecorder()
	if strings.HasSuffix(r.Path, "/events") {
		cancel()
	}
	s.e.ServeHTTP(rec, req)
	return rec.Code
}

func newAuthModesServer(ownership string, opts ...config.AppOption) (*application.Application, *authModesServer) {
	dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
	Expect(err).NotTo(HaveOccurred())
	models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
	Expect(os.Mkdir(models, 0o750)).To(Succeed())
	Expect(os.Mkdir(backends, 0o750)).To(Succeed())
	state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	root := filepath.Join(dir, "addons")
	transport, address, err := gateway.NewLoopbackCarrierConfig("local", root)
	Expect(err).NotTo(HaveOccurred())
	all := append([]config.AppOption{
		config.WithContext(ctx),
		config.WithSystemState(state),
		config.WithAddons(config.AddonConfig{
			Enabled:    true,
			InstanceID: "local", OwnerID: "local", OwnerAddress: transport.OwnerAddress,
			StorageID: "local", Root: root, ListenAddress: address, Transport: transport,
			OwnershipMode: ownership,
			Host:          contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH},
		}),
	}, opts...)
	app, err := application.New(all...)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = app.Shutdown() })
	e, err := API(app)
	Expect(err).NotTo(HaveOccurred())
	return app, &authModesServer{e: e, creds: map[string]authModesCredential{}}
}

// adminOnlyAddonRoutes are the add-on routes a user without the administrator
// role never reaches, whatever the credential. The list restates the contract
// on purpose: a route that loses its administrator check drops out of the 403
// below instead of being followed silently.
var adminOnlyAddonRoutes = []string{
	"GET /api/addons/apps",
	"GET /api/addons/:id/recovery",
	"POST /api/addons/apps/:id/start",
	"POST /api/addons/apps/:id/stop",
	"POST /api/addons/:id/upgrade",
	"POST /api/addons/:id/purge",
	"POST /api/addons/:id/update/check",
	"POST /api/addons/:id/recovery/backup",
	"POST /api/addons/:id/recovery/restore",
	"POST /api/addons/:id/recovery/verify",
	"POST /api/addons/:id/recovery/prune",
	"POST /api/addons/:id/recovery/delete-backup",
}

// A route's answer in each mode. The expectations below do not depend on what
// the handler answers once it is reached (400, 404 and 409 all mean "reached");
// they pin who is let in.
func isDenied(code int) bool { return code == http.StatusUnauthorized || code == http.StatusForbidden }

// The new chats and add-on routes take their caller from the authentication
// middleware. This runs every one of them in each way a deployment can be
// configured, with the real server and its real middleware chain: no
// authentication (local owner), shared API keys, and accounts with sessions
// and per-user API keys.
var _ = Describe("Route auth modes", func() {
	BeforeEach(func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
	})

	It("registers the routes the matrix covers", func() {
		_, s := newAuthModesServer("local-owner")
		paths := map[string]bool{}
		for _, r := range s.routesUnderTest() {
			paths[r.Method+" "+r.Path] = true
		}
		Expect(len(paths)).To(BeNumerically(">", 100))
		for _, want := range append([]string{"GET /api/chats", "POST /api/chats", "GET /api/addons/builtin"}, adminOnlyAddonRoutes...) {
			Expect(paths).To(HaveKey(want))
		}
	})

	It("lets the local owner reach every route and refuses nothing at the door", func() {
		_, s := newAuthModesServer("local-owner")
		denied := []string{}
		for _, r := range s.routesUnderTest() {
			if code := s.status(r, ""); code == http.StatusUnauthorized {
				denied = append(denied, r.Method+" "+r.Path)
			}
		}
		Expect(denied).To(BeEmpty(), "routes that ask the local owner to sign in")
	})

	It("refuses an anonymous caller on every route when shared API keys are configured, and accepts the key", func() {
		_, s := newAuthModesServer("local-owner", config.WithApiKeys([]string{authModesLegacyKey}))
		s.creds["key"] = authModesCredential{header: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+authModesLegacyKey) }}
		s.creds["wrong"] = authModesCredential{header: func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-the-key") }}
		problems := []string{}
		for _, r := range s.routesUnderTest() {
			name := r.Method + " " + r.Path
			if code := s.status(r, ""); code != http.StatusUnauthorized {
				problems = append(problems, name+": anonymous got "+http.StatusText(code))
			}
			if code := s.status(r, "wrong"); code != http.StatusUnauthorized {
				problems = append(problems, name+": a wrong key got "+http.StatusText(code))
			}
			if code := s.status(r, "key"); code == http.StatusUnauthorized {
				problems = append(problems, name+": the key was refused")
			}
		}
		Expect(problems).To(BeEmpty())
	})

	It("treats the shared API key as the local owner on the add-on routes", func() {
		_, local := newAuthModesServer("local-owner")
		_, keyed := newAuthModesServer("local-owner", config.WithApiKeys([]string{authModesLegacyKey}))
		keyed.creds["key"] = authModesCredential{header: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+authModesLegacyKey) }}
		problems := []string{}
		for _, r := range local.routesUnderTest() {
			if !strings.HasPrefix(r.Path, "/api/addons") {
				continue
			}
			if a, b := local.status(r, ""), keyed.status(r, "key"); a != b {
				problems = append(problems, fmt.Sprintf("%s %s: local owner %d, shared key %d", r.Method, r.Path, a, b))
			}
		}
		Expect(problems).To(BeEmpty())
	})

	Context("with accounts", func() {
		var server *authModesServer

		BeforeEach(func() {
			app, s := newAuthModesServer("named-user",
				config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(authModesHMACSecret))
			db := app.AuthDB()
			Expect(db).NotTo(BeNil())
			for _, id := range []string{"alice", "carol", "root"} {
				role := auth.RoleUser
				if id == "root" {
					role = auth.RoleAdmin
				}
				Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
				session, err := auth.CreateSession(db, id, authModesHMACSecret)
				Expect(err).NotTo(HaveOccurred())
				s.creds[id] = authModesCredential{header: func(r *http.Request) {
					r.AddCookie(&http.Cookie{Name: "session", Value: session})
					// The browser protection of the add-on routes wants a same-origin fetch.
					r.Header.Set("Sec-Fetch-Site", "same-origin")
				}}
				plain, _, err := auth.CreateAPIKey(db, id, "test", role, authModesHMACSecret, nil)
				Expect(err).NotTo(HaveOccurred())
				s.creds[id+"-key"] = authModesCredential{header: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+plain) }}
			}
			// carol had the default-on chats feature revoked by an administrator.
			Expect(auth.UpdateUserPermissions(db, "carol", auth.PermissionMap{auth.FeatureChats: false})).To(Succeed())
			// agents, triggers, connectors and pages are off by default for a user; alice has them.
			Expect(auth.UpdateUserPermissions(db, "alice", auth.PermissionMap{
				auth.FeatureAgents: true, auth.FeatureTriggers: true, auth.FeatureConnectors: true, auth.FeaturePages: true,
			})).To(Succeed())
			server = s
		})

		It("refuses an anonymous caller on every route", func() {
			problems := []string{}
			for _, r := range server.routesUnderTest() {
				if code := server.status(r, ""); code != http.StatusUnauthorized {
					problems = append(problems, r.Method+" "+r.Path+": got "+http.StatusText(code))
				}
			}
			Expect(problems).To(BeEmpty())
		})

		It("lets a signed-in user reach every chats route with a session and with an API key, and answers both the same way", func() {
			problems := []string{}
			count := 0
			for _, r := range server.routesUnderTest() {
				if !strings.HasPrefix(r.Path, "/api/chats") {
					continue
				}
				count++
				name := r.Method + " " + r.Path
				users := []string{"alice", "root"}
				if strings.HasPrefix(r.Path, "/api/chats/admin/") {
					// The administrator routes are closed to a user who is not one.
					for _, cred := range []string{"alice", "alice-key"} {
						if code := server.status(r, cred); code != http.StatusForbidden {
							problems = append(problems, fmt.Sprintf("%s: %s got %d, not 403", name, cred, code))
						}
					}
					users = []string{"root"}
				}
				for _, user := range users {
					session, key := server.status(r, user), server.status(r, user+"-key")
					if isDenied(session) || isDenied(key) {
						problems = append(problems, fmt.Sprintf("%s: %s got %d with a session and %d with a key", name, user, session, key))
					}
				}
			}
			Expect(count).To(BeNumerically(">", 50))
			Expect(problems).To(BeEmpty())
		})

		It("closes every chats route to a user whose chats feature was revoked", func() {
			problems := []string{}
			for _, r := range server.routesUnderTest() {
				if !strings.HasPrefix(r.Path, "/api/chats") {
					continue
				}
				for _, cred := range []string{"carol", "carol-key"} {
					if code := server.status(r, cred); code != http.StatusForbidden {
						problems = append(problems, fmt.Sprintf("%s %s: %s got %d", r.Method, r.Path, cred, code))
					}
				}
			}
			Expect(problems).To(BeEmpty())
		})

		It("answers a session and an API key of the same user alike on every add-on route", func() {
			problems := []string{}
			for _, r := range server.routesUnderTest() {
				if !strings.HasPrefix(r.Path, "/api/addons") {
					continue
				}
				for _, user := range []string{"alice", "root"} {
					if session, key := server.status(r, user), server.status(r, user+"-key"); session != key {
						problems = append(problems, fmt.Sprintf("%s %s: %s got %d with a session and %d with a key", r.Method, r.Path, user, session, key))
					}
				}
			}
			Expect(problems).To(BeEmpty())
		})

		It("keeps the administrator routes of the add-ons from a user who is not an administrator", func() {
			byName := map[string]*echo.Route{}
			for _, r := range server.routesUnderTest() {
				byName[r.Method+" "+r.Path] = r
			}
			problems := []string{}
			for _, name := range adminOnlyAddonRoutes {
				r, ok := byName[name]
				Expect(ok).To(BeTrue(), name)
				for _, cred := range []string{"alice", "alice-key", "carol"} {
					if code := server.status(r, cred); code != http.StatusForbidden {
						problems = append(problems, fmt.Sprintf("%s: %s got %d", name, cred, code))
					}
				}
				for _, cred := range []string{"root", "root-key"} {
					if code := server.status(r, cred); isDenied(code) {
						problems = append(problems, fmt.Sprintf("%s: %s got %d", name, cred, code))
					}
				}
			}
			Expect(problems).To(BeEmpty())
		})

		It("lets every user read the add-on pages that every user sees", func() {
			for _, path := range []string{"/api/addons/builtin", "/api/addons/pins"} {
				r := &echo.Route{Method: http.MethodGet, Path: path}
				for _, cred := range []string{"alice", "alice-key", "carol", "root"} {
					Expect(server.status(r, cred)).To(Equal(http.StatusOK), path+" for "+cred)
				}
			}
		})
	})
})
