package http_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Vite content-hashes the React bundle filenames, so a given /assets/ URL can
// never change content. Serving those without Cache-Control meant the browser
// re-downloaded the whole ~1.8 MB bundle on every navigation, with not even a
// conditional request available to turn it into a 304. index.html carries the
// hashed filenames, so it must stay uncached or a deploy would never be
// picked up.
var _ = Describe("Static asset caching", Ordered, func() {
	var (
		app    *echo.Echo
		tmpdir string
		cancel context.CancelFunc
		asset  string
	)

	BeforeAll(func() {
		var err error
		tmpdir, err = os.MkdirTemp("", "static-cache-")
		Expect(err).ToNot(HaveOccurred())

		modelDir := filepath.Join(tmpdir, "models")
		Expect(os.Mkdir(modelDir, 0750)).To(Succeed())
		bDir := filepath.Join(tmpdir, "backends")
		Expect(os.Mkdir(bDir, 0750)).To(Succeed())

		var c context.Context
		c, cancel = context.WithCancel(context.Background())

		systemState, err := system.GetSystemState(
			system.WithBackendPath(bDir),
			system.WithModelPath(modelDir),
		)
		Expect(err).ToNot(HaveOccurred())

		appInst, err := application.New(
			config.WithContext(c),
			config.WithSystemState(systemState),
		)
		Expect(err).ToNot(HaveOccurred())

		app, err = API(appInst)
		Expect(err).ToNot(HaveOccurred())

		// Pick a real filename out of the embedded build so the spec exercises
		// the same handler path a browser would hit.
		asset = firstEmbeddedAsset(app)
		Expect(asset).ToNot(BeEmpty(), "the embedded React build must contain at least one asset")
	})

	AfterAll(func() {
		cancel()
		Expect(os.RemoveAll(tmpdir)).To(Succeed())
	})

	do := func(path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("serves content-hashed assets as immutable for a year", func() {
		rec := do("/assets/"+asset, nil)

		Expect(rec.Code).To(Equal(http.StatusOK))
		cc := rec.Header().Get("Cache-Control")
		Expect(cc).To(ContainSubstring("public"))
		Expect(cc).To(ContainSubstring("max-age=31536000"))
		Expect(cc).To(ContainSubstring("immutable"))
	})

	It("keeps index.html uncached so a deploy is picked up", func() {
		rec := do("/app", nil)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-cache"))
	})

	// A tab that runs an old build asks for chunks a new build no longer has.
	// The reload that recovers it must fetch a fresh index.html whatever the
	// address, so every route that answers with index.html is uncached.
	It("keeps index.html uncached on app subpaths and on the SPA fallback", func() {
		for _, path := range []string{"/app/chats/c1", "/app/settings"} {
			rec := do(path, nil)
			Expect(rec.Code).To(Equal(http.StatusOK), path)
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-cache"), path)
		}

		// /login has no server route: the 404 handler serves the SPA for a
		// browser navigation.
		rec := do("/login", map[string]string{"Accept": "text/html,application/xhtml+xml"})
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(ContainSubstring("text/html"))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-cache"))
	})

	// The chunk a stale tab asks for after a deploy: a plain 404 that the
	// browser must not keep, never index.html in its place (a module script
	// served as HTML fails with a MIME error instead of a clean 404).
	It("answers a missing hashed asset with an uncacheable 404", func() {
		rec := do("/assets/InboxPanel-gone0000.js", map[string]string{"Accept": "*/*"})

		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(rec.Header().Get("Cache-Control")).ToNot(ContainSubstring("immutable"))
		Expect(rec.Header().Get("Cache-Control")).ToNot(ContainSubstring("max-age"))
		Expect(rec.Header().Get("Content-Type")).ToNot(ContainSubstring("text/html"))
	})

	It("does not mark the unhashed locale files immutable", func() {
		rec := do("/locales/en/common.json", nil)

		if rec.Code == http.StatusOK {
			Expect(rec.Header().Get("Cache-Control")).ToNot(ContainSubstring("immutable"))
		}
	})

	It("gzips the asset when the client accepts it", func() {
		plain := do("/assets/"+asset, nil)
		gzipped := do("/assets/"+asset, map[string]string{"Accept-Encoding": "gzip"})

		Expect(gzipped.Code).To(Equal(http.StatusOK))
		// Small assets fall below the compression threshold; only assert the
		// shrink when the middleware actually engaged.
		if gzipped.Header().Get("Content-Encoding") == "gzip" {
			Expect(gzipped.Body.Len()).To(BeNumerically("<", plain.Body.Len()))
		}
		Expect(plain.Header().Get("Content-Encoding")).To(BeEmpty())
	})
})

// firstEmbeddedAsset asks the running app for the asset listing indirectly:
// the SPA index references its own bundles, so parsing it yields a filename
// that is guaranteed to exist in the embedded build.
func firstEmbeddedAsset(app *echo.Echo) string {
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		return ""
	}
	const marker = "/assets/"
	idx := strings.Index(string(body), marker)
	if idx < 0 {
		return ""
	}
	rest := string(body)[idx+len(marker):]
	end := strings.IndexAny(rest, `"'`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
