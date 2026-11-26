package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/clustersettings"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/system"
)

// Two frontends of one cluster: the memory settings saved through one are in
// force on the other, never written to a frontend's file, and a save from a
// stale read is refused.
var _ = Describe("Settings endpoints with cluster memory settings", func() {
	type frontend struct {
		app *application.Application
		dir string
		e   *echo.Echo
	}

	var (
		ctx    context.Context
		cancel context.CancelFunc
		bus    *testutil.FakeBus
		store  *clustersettings.DBStore
	)

	newFrontend := func() *frontend {
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(
			system.WithModelPath(filepath.Join(dir, "models")),
			system.WithBackendPath(filepath.Join(dir, "backends")),
		)
		Expect(err).ToNot(HaveOccurred())
		app, err := application.New(config.WithContext(ctx), config.WithSystemState(st))
		Expect(err).ToNot(HaveOccurred())
		app.ApplicationConfig().DynamicConfigsDir = dir
		Expect(app.StartClusterSettings(ctx, bus, store)).To(Succeed())
		DeferCleanup(func() { _ = app.ClusterSettings().Close() })
		e := echo.New()
		e.GET("/api/settings", GetSettingsEndpoint(app))
		e.POST("/api/settings", UpdateSettingsEndpoint(app))
		return &frontend{app: app, dir: dir, e: e}
	}

	do := func(f *frontend, method, body string) (*httptest.ResponseRecorder, map[string]any) {
		req := httptest.NewRequest(method, "/api/settings", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		f.e.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		bus = testutil.NewFakeBus()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		store, err = clustersettings.NewDBStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	It("saves on frontend A and enforces on frontend B", func() {
		a := newFrontend()
		b := newFrontend()

		rec, _ := do(a, http.MethodPost, `{"model_swap": false, "max_active_backends": 2, "threads": 3}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		Expect(b.app.ApplicationConfig().ModelSwapEnabled()).To(BeFalse())
		Expect(b.app.ApplicationConfig().MaxActiveBackends).To(Equal(2))
		Expect(b.app.ModelLoader().ModelSwapEnabled()).To(BeFalse())
		Expect(b.app.ApplicationConfig().Threads).ToNot(Equal(3), "threads is a frontend setting")

		_, got := do(b, http.MethodGet, "")
		Expect(got["model_swap"]).To(Equal(false))
		Expect(got["memory_scope"]).To(Equal("cluster"))
		Expect(got["memory_revision"]).To(BeNumerically(">", 0))

		persisted, err := os.ReadFile(filepath.Join(a.dir, "runtime_settings.json"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(persisted)).ToNot(ContainSubstring("model_swap"))
		Expect(string(persisted)).To(ContainSubstring("threads"))
	})

	It("refuses a save made from a stale read with 409", func() {
		a := newFrontend()
		b := newFrontend()
		_, read := do(b, http.MethodGet, "")
		rev := read["memory_revision"]

		rec, _ := do(a, http.MethodPost, `{"model_swap": false}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		body, _ := json.Marshal(map[string]any{"max_active_backends": 4, "memory_revision": rev})
		rec, _ = do(b, http.MethodPost, string(body))

		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(a.app.ApplicationConfig().MaxActiveBackends).To(Equal(0))
	})
})
