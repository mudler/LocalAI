// SPDX-License-Identifier: MIT

package routes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// An owner and frontends run as separate applications with separate data
// paths. They share the carrier key set and the owner address only.
var _ = Describe("library routes through a split owner", func() {
	var (
		keys    string
		address string
		port    string
		stops   map[*application.Application]context.CancelFunc
	)

	newApp := func(role, dataPath string, official bool) *application.Application {
		models, backends := filepath.Join(dataPath, "models"), filepath.Join(dataPath, "backends")
		Expect(os.MkdirAll(models, 0750)).To(Succeed())
		Expect(os.MkdirAll(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		transport, err := gateway.LoadCarrierConfig(keys, role, address)
		Expect(err).NotTo(HaveOccurred())
		cfg := config.AddonConfig{
			Enabled:    true,
			InstanceID: transport.InstanceID, OwnerID: gateway.OwnerIdentity, OwnerAddress: address,
			OfficialLibrary: true, OfficialBackendLibrary: true,
			StorageID: "local", Transport: transport, OwnershipMode: auth.AddonModeLocalOwner,
			Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH},
		}
		if role == "frontend" {
			cfg.Frontend = true
		} else {
			cfg.Root = filepath.Join(dataPath, "addons")
			cfg.ListenAddress = "127.0.0.1:" + port
			Expect(os.MkdirAll(cfg.Root, 0700)).To(Succeed())
		}
		ctx, cancel := context.WithCancel(context.Background())
		app, err := application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(dataPath),
			config.WithOfficialLibraries(official), config.WithAddons(cfg),
			config.WithBackendGalleries(config.DefaultBackendGalleriesJSON), config.WithStringGalleries(config.DefaultGalleriesJSON))
		Expect(err).NotTo(HaveOccurred())
		stops[app] = cancel
		DeferCleanup(func() { cancel(); _ = app.Shutdown() })
		return app
	}
	stop := func(app *application.Application) {
		stops[app]()
		_ = app.Shutdown()
	}
	serve := func(app *application.Application, user *auth.User) *echo.Echo {
		e := echo.New()
		pass := func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if user != nil {
					c.Set("auth_user", user)
				}
				return next(c)
			}
		}
		routes.RegisterLibrarySourceRoutes(e, app, app.ApplicationConfig(), pass)
		e.POST("/api/settings", localai.UpdateSettingsEndpoint(app))
		return e
	}
	send := func(e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		e.ServeHTTP(w, r)
		return w
	}
	urls := func(g []config.Gallery) []string {
		var out []string
		for _, x := range g {
			out = append(out, x.URL)
		}
		return out
	}
	envelope := func(w *httptest.ResponseRecorder) (message, code string) {
		var b struct {
			Error struct{ Message, Code string }
		}
		ExpectWithOffset(1, json.Unmarshal(w.Body.Bytes(), &b)).To(Succeed(), w.Body.String())
		return b.Error.Message, b.Error.Code
	}

	BeforeEach(func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
		stops = map[*application.Application]context.CancelFunc{}
		keysDir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		keys = filepath.Join(keysDir, "keys")
		Expect(gateway.GenerateCarrierKeys(keys, []string{"127.0.0.1"})).To(Succeed())
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		port = strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		Expect(l.Close()).To(Succeed())
		address = "https://127.0.0.1:" + port
	})

	dir := func() string {
		d, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		return d
	}

	It("lists the sources through the owner, adds one on the owner, and shows it on every frontend after a refresh", func() {
		owner := newApp("owner", dir(), true)
		frontendData := dir()
		first, second := newApp("frontend", frontendData, true), newApp("frontend", dir(), true)
		e := serve(first, nil)

		w := send(e, http.MethodGet, "/api/library/sources", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var list struct {
			Sources           []struct{ ID, URL string }
			OfficialLibraries bool
		}
		Expect(json.Unmarshal(w.Body.Bytes(), &list)).To(Succeed())
		Expect(list.Sources).NotTo(BeEmpty())
		Expect(list.OfficialLibraries).To(BeTrue())

		const added = "https://example.org/extra-models.yaml"
		w = send(e, http.MethodPost, "/api/library/sources", `{"kind":"model","url":"`+added+`","format":"gallery"}`)
		Expect(w.Code).To(Equal(http.StatusCreated), w.Body.String())
		// The owner holds the source; the frontend wrote nothing.
		var onOwner []string
		for _, s := range owner.AddonSources().Entries() {
			onOwner = append(onOwner, s.URL)
		}
		Expect(onOwner).To(ContainElement(added))
		Expect(filepath.Join(frontendData, "addons")).NotTo(BeADirectory())
		// The frontend that forwarded the change follows at once; the other one
		// at its next refresh.
		Expect(urls(first.ApplicationConfig().Galleries)).To(ContainElement(added))
		Expect(urls(second.ApplicationConfig().Galleries)).NotTo(ContainElement(added))
		Expect(second.RefreshLibraries(context.Background())).To(Succeed())
		Expect(urls(second.ApplicationConfig().Galleries)).To(ContainElement(added))
		Expect(second.LibraryListsStale()).To(BeFalse())
	})

	It("answers the statuses of the owner's own routes", func() {
		newApp("owner", dir(), true)
		e := serve(newApp("frontend", dir(), true), nil)
		body := `{"kind":"model","url":"https://example.org/dup.yaml","id":"dup"}`
		Expect(send(e, http.MethodPost, "/api/library/sources", body).Code).To(Equal(http.StatusCreated))
		Expect(send(e, http.MethodPost, "/api/library/sources", body).Code).To(Equal(http.StatusConflict))
		Expect(send(e, http.MethodPost, "/api/library/sources/nope/enable", "").Code).To(Equal(http.StatusNotFound))
		Expect(send(e, http.MethodPost, "/api/library/sources", `{"kind":"x","url":"https://example.org/a"}`).Code).To(Equal(http.StatusBadRequest))
		Expect(send(e, http.MethodPost, "/api/library/sources/dup/disable", "").Code).To(Equal(http.StatusOK))
		Expect(send(e, http.MethodDelete, "/api/library/sources/dup", "").Code).To(Equal(http.StatusOK))
	})

	It("refuses a write with the 503 envelope while the owner is down", func() {
		owner := newApp("owner", dir(), true)
		frontend := newApp("frontend", dir(), true)
		e := serve(frontend, nil)
		Expect(send(e, http.MethodGet, "/api/library/sources", "").Code).To(Equal(http.StatusOK))
		stop(owner)
		for _, c := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/library/sources", `{"kind":"model","url":"https://example.org/a.yaml"}`},
			{http.MethodGet, "/api/library/sources", ""},
		} {
			w := send(e, c.method, c.path, c.body)
			Expect(w.Code).To(Equal(http.StatusServiceUnavailable), c.method)
			message, code := envelope(w)
			Expect(message).To(Equal("the add-on owner is not reachable"))
			Expect(code).To(Equal("unavailable"))
		}
	})

	It("refuses to change official_libraries on a frontend with 409", func() {
		newApp("owner", dir(), true)
		frontend := newApp("frontend", dir(), true)
		e := serve(frontend, nil)
		w := send(e, http.MethodPost, "/api/settings", `{"official_libraries": false}`)
		Expect(w.Code).To(Equal(http.StatusConflict), w.Body.String())
		Expect(frontend.ApplicationConfig().OfficialLibraries).To(BeTrue())
	})

	It("rebuilds the gallery lists of a frontend from the owner after official_libraries changes there", func() {
		owner := newApp("owner", dir(), true)
		frontend := newApp("frontend", dir(), true)
		same := func() {
			ExpectWithOffset(1, frontend.RefreshLibraries(context.Background())).To(Succeed())
			ExpectWithOffset(1, urls(frontend.ApplicationConfig().Galleries)).To(Equal(urls(owner.ApplicationConfig().Galleries)))
			ExpectWithOffset(1, urls(frontend.ApplicationConfig().BackendGalleries)).To(Equal(urls(owner.ApplicationConfig().BackendGalleries)))
			ExpectWithOffset(1, frontend.ApplicationConfig().OfficialLibraries).To(Equal(owner.ApplicationConfig().OfficialLibraries))
		}
		// The owner applies the shipped backend library when it is on.
		Expect(owner.ApplyLibraries()).To(Succeed())
		same()
		on := len(owner.ApplicationConfig().BackendGalleries)

		// What the settings handler does on the owner.
		owner.ApplicationConfig().OfficialLibraries = false
		Expect(owner.ApplyLibraries()).To(Succeed())
		Expect(owner.ApplicationConfig().BackendGalleries).To(HaveLen(on - 1))
		same()
		Expect(frontend.ApplicationConfig().OfficialLibraries).To(BeFalse())

		// And back on.
		owner.ApplicationConfig().OfficialLibraries = true
		Expect(owner.ApplyLibraries()).To(Succeed())
		same()
		Expect(frontend.ApplicationConfig().BackendGalleries).To(HaveLen(on))
	})

	It("keeps the last gallery lists and reports them stale while the owner is down", func() {
		owner := newApp("owner", dir(), true)
		frontend := newApp("frontend", dir(), true)
		Expect(frontend.RefreshLibraries(context.Background())).To(Succeed())
		before := urls(frontend.ApplicationConfig().Galleries)
		Expect(before).NotTo(BeEmpty())
		stop(owner)
		Expect(frontend.RefreshLibraries(context.Background())).NotTo(Succeed())
		Expect(frontend.LibraryListsStale()).To(BeTrue())
		Expect(urls(frontend.ApplicationConfig().Galleries)).To(Equal(before))
	})

	It("refuses a legacy API key on a frontend library route", func() {
		newApp("owner", dir(), true)
		e := serve(newApp("frontend", dir(), true), &auth.User{ID: "legacy-api-key", Role: auth.RoleAdmin})
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/library/sources"},
			{http.MethodPost, "/api/library/sources"},
			{http.MethodDelete, "/api/library/sources/x"},
		} {
			Expect(send(e, c.method, c.path, `{"kind":"model","url":"https://example.org/a.yaml"}`).Code).To(Equal(http.StatusUnauthorized), fmt.Sprint(c))
		}
	})
})
