package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
	"github.com/mudler/LocalAI/pkg/system"
)

type recordingModelUpgrader struct {
	mu   sync.Mutex
	opts []galleryop.ModelUpgradeOptions
	done chan string
}

func (r *recordingModelUpgrader) UpgradeModel(_ context.Context, _, name string, opts galleryop.ModelUpgradeOptions, _ func(*galleryop.OpStatus)) error {
	r.mu.Lock()
	r.opts = append(r.opts, opts)
	r.mu.Unlock()
	r.done <- name
	return nil
}

var _ = Describe("Model update routes", func() {
	var (
		e    *echo.Echo
		dir  string
		rec  *recordingModelUpgrader
		deps ModelUpgradeDeps
	)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w
	}
	installed := func(name string, j *gallery.Journal) {
		Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte("name: "+name+"\n"), 0600)).To(Succeed())
		Expect(gallery.WriteSidecar(dir, name, &gallery.ModelConfig{Name: name, Version: gallery.SidecarVersion,
			Origin: &gallery.Origin{Gallery: "g", EntryName: name, EntryDigest: "sha256:a"}, Upgrade: j})).To(Succeed())
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		gs := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		rec = &recordingModelUpgrader{done: make(chan string, 2)}
		gs.SetModelUpgrader(rec)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(gs.Start(ctx, nil, nil)).To(Succeed())
		up := &modelupdates.Upgrader{SystemState: state}
		svc := modelupdates.New(modelupdates.Options{SystemState: state, ListGallery: func(context.Context, bool) ([]*gallery.GalleryModel, error) { return nil, nil }})
		deps = ModelUpgradeDeps{Gallery: gs, Upgrader: func() *modelupdates.Upgrader { return up }, Updates: func() *modelupdates.Service { return svc }}
		e = echo.New()
		e.POST("/api/models/upgrade/:name", UpgradeModelEndpoint(deps))
		e.POST("/api/models/:name/upgrade/retry", RetryModelUpgradeEndpoint(deps))
		e.GET("/api/models/updates", ModelUpdatesEndpoint(func() ModelUpdatesProvider { return svc }))
	})

	It("queues an upgrade with its options and returns the operation", func() {
		installed("tiny", nil)
		w := do(http.MethodPost, "/api/models/upgrade/tiny", `{"config_only":true,"choices":{"context_size":"mine"}}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var resp schema.ModelUpgradeResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.ID).ToNot(BeEmpty())
		Expect(resp.JobID).To(Equal(resp.ID))
		Expect(resp.StatusURL).To(HaveSuffix("models/jobs/" + resp.ID))
		Eventually(rec.done).Should(Receive(Equal("tiny")))
		Expect(rec.opts[0]).To(Equal(galleryop.ModelUpgradeOptions{ConfigOnly: true, Choices: map[string]string{"context_size": "mine"}}))
	})

	It("answers 404 for a model that is not installed", func() {
		Expect(do(http.MethodPost, "/api/models/upgrade/none", "").Code).To(Equal(http.StatusNotFound))
	})

	It("answers 422 for a model without a recorded origin", func() {
		Expect(os.WriteFile(filepath.Join(dir, "old.yaml"), []byte("name: old\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(gallery.SidecarPath(dir, "old"), []byte("name: old\n"), 0600)).To(Succeed())
		Expect(do(http.MethodPost, "/api/models/upgrade/old", "").Code).To(Equal(http.StatusUnprocessableEntity))
	})

	It("answers 409 while an upgrade runs, and to a retry with nothing to retry", func() {
		installed("busy", &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseStaging})
		Expect(do(http.MethodPost, "/api/models/upgrade/busy", "").Code).To(Equal(http.StatusConflict))
		installed("fine", nil)
		Expect(do(http.MethodPost, "/api/models/fine/upgrade/retry", "").Code).To(Equal(http.StatusConflict))
	})

	It("answers 409 for a held model, to an upgrade and to a retry", func() {
		installed("held", &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseFailed})
		sc, err := gallery.ReadSidecar(dir, "held")
		Expect(err).ToNot(HaveOccurred())
		sc.Hold = true
		Expect(gallery.WriteSidecar(dir, "held", sc)).To(Succeed())
		w := do(http.MethodPost, "/api/models/upgrade/held", "")
		Expect(w.Code).To(Equal(http.StatusConflict))
		Expect(w.Body.String()).To(ContainSubstring("release the hold"))
		Expect(do(http.MethodPost, "/api/models/held/upgrade/retry", "").Code).To(Equal(http.StatusConflict))
	})

	It("does not answer an unexpected error with its text, which can name server paths", func() {
		Expect(os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("name: broken\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(gallery.SidecarPath(dir, "broken"), []byte("version: [\n"), 0600)).To(Succeed())
		w := do(http.MethodPost, "/api/models/upgrade/broken", "")
		Expect(w.Code).To(Equal(http.StatusInternalServerError))
		Expect(w.Body.String()).ToNot(ContainSubstring(dir))
		Expect(w.Body.String()).To(ContainSubstring("server log"))
	})

	It("rejects a choice that is neither mine nor gallery", func() {
		installed("tiny", nil)
		Expect(do(http.MethodPost, "/api/models/upgrade/tiny", `{"choices":{"k":"both"}}`).Code).To(Equal(http.StatusBadRequest))
	})

	It("queues a retry of a failed upgrade", func() {
		installed("failed", &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseRolledBack, Error: "load failed"})
		Expect(do(http.MethodPost, "/api/models/failed/upgrade/retry", "").Code).To(Equal(http.StatusOK))
		Eventually(rec.done).Should(Receive(Equal("failed")))
		Expect(rec.opts[0].Retry).To(BeTrue())
	})

	Context("with upgrade runs", func() {
		var h *upgraderuntest.Harness

		BeforeEach(func() {
			var err error
			h, err = upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(h.Service.Close)
			deps.Runs = UpgradeRunDeps{Runs: func() *upgraderun.Service { return h.Service }}
			e = echo.New()
			e.POST("/api/models/upgrade/:name", UpgradeModelEndpoint(deps))
			installed("tiny", nil)
			h.Models.Add(upgraderun.Candidate{Kind: upgraderun.KindModel, Name: "tiny", From: "d1", To: "d2"})
		})

		It("starts a run of one item and keeps the fields of the answer", func() {
			w := do(http.MethodPost, "/api/models/upgrade/tiny", "")
			Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
			var resp ModelUpgradeRunResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.JobID).To(Equal(h.Starter.Job("model:tiny")))
			Expect(resp.ID).To(Equal(resp.JobID))
			Expect(resp.StatusURL).To(HaveSuffix("models/jobs/" + resp.ID))
			Expect(resp.Message).To(Equal("Upgrade started"))
			Expect(resp.Run).NotTo(BeNil())
			Expect(resp.Run.Items).To(HaveLen(1))
			Consistently(rec.done, 100*time.Millisecond).ShouldNot(Receive(), "the run's starter queued it, not the route")
		})

		It("answers 202 with the run, and no job, when the operation is not accepted within the wait", func() {
			gate := make(chan struct{})
			DeferCleanup(func() { close(gate) })
			h.Starter.Block(gate)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, "/api/models/upgrade/tiny", nil).WithContext(ctx)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusAccepted), w.Body.String())
			var body SingleUpgradePending
			Expect(json.Unmarshal(w.Body.Bytes(), &body)).To(Succeed())
			Expect(body.Code).To(Equal("pending"))
			Expect(body.RunID).NotTo(BeEmpty())
			Expect(w.Body.String()).NotTo(ContainSubstring("Upgrade started"))
			Expect(w.Body.String()).NotTo(ContainSubstring(`"jobID"`))
		})

		It("answers 409 run_active while another run runs", func() {
			Expect(do(http.MethodPost, "/api/models/upgrade/tiny", "").Code).To(Equal(http.StatusOK))
			w := do(http.MethodPost, "/api/models/upgrade/tiny", "")
			Expect(w.Code).To(Equal(http.StatusConflict))
			var resp schema.ModelUpgradeError
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Code).To(Equal(upgraderun.CodeRunActive))
		})

		It("refuses every direct upgrade with 409 run_active while a run runs", func() {
			e.POST("/api/models/:name/upgrade/retry", RetryModelUpgradeEndpoint(deps))
			installed("failed", &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseRolledBack, Error: "load failed"})
			Expect(do(http.MethodPost, "/api/models/upgrade/tiny", "").Code).To(Equal(http.StatusOK))
			for _, req := range []struct{ path, body string }{
				{"/api/models/upgrade/tiny", `{"config_only":true}`},
				{"/api/models/upgrade/tiny", `{"choices":{"context_size":"mine"}}`},
				{"/api/models/failed/upgrade/retry", ""},
			} {
				w := do(http.MethodPost, req.path, req.body)
				Expect(w.Code).To(Equal(http.StatusConflict), req.path+" "+req.body+": "+w.Body.String())
				var resp schema.ModelUpgradeError
				Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
				Expect(resp.Code).To(Equal(upgraderun.CodeRunActive), req.path+" "+req.body)
			}
			Consistently(rec.done, 100*time.Millisecond).ShouldNot(Receive(), "nothing was queued beside the run")
		})

		It("keeps an upgrade with options direct", func() {
			w := do(http.MethodPost, "/api/models/upgrade/tiny", `{"config_only":true}`)
			Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
			Eventually(rec.done).Should(Receive(Equal("tiny")))
			Expect(h.Starter.Job("model:tiny")).To(BeEmpty())
		})

		It("queues directly when the run's check does not list the model", func() {
			h.Models.Remove("model:tiny")
			w := do(http.MethodPost, "/api/models/upgrade/tiny", "")
			Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
			Eventually(rec.done).Should(Receive(Equal("tiny")))
		})
	})

	It("answers 503 when no model gallery is configured", func() {
		bare := echo.New()
		bare.POST("/api/models/upgrade/:name", UpgradeModelEndpoint(ModelUpgradeDeps{}))
		w := httptest.NewRecorder()
		bare.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/upgrade/x", nil))
		Expect(w.Code).To(Equal(http.StatusServiceUnavailable))
	})

	It("reports a disabled source without a model gallery", func() {
		bare := echo.New()
		bare.GET("/api/models/updates", ModelUpdatesEndpoint(func() ModelUpdatesProvider { return nil }))
		w := httptest.NewRecorder()
		bare.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models/updates", nil))
		var snap modelupdates.Snapshot
		Expect(json.Unmarshal(w.Body.Bytes(), &snap)).To(Succeed())
		Expect(snap.Sources[modelupdates.SourceGallery].State).To(Equal(backendupdates.StateDisabled))
		Expect(snap.Items).To(BeEmpty())
	})
})

var _ = Describe("Model hold and old version routes", func() {
	var (
		e   *echo.Echo
		dir string
	)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		up := &modelupdates.Upgrader{SystemState: state}
		deps := ModelUpgradeDeps{Upgrader: func() *modelupdates.Upgrader { return up }}
		e = echo.New()
		e.PUT("/api/models/:name/hold", SetModelHoldEndpoint(deps))
		e.POST("/api/models/:name/remove-old", RemoveOldModelVersionEndpoint(deps))
		Expect(os.WriteFile(filepath.Join(dir, "tiny.yaml"), []byte("name: tiny\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "tiny.gguf.prev-12345678"), []byte("old"), 0600)).To(Succeed())
		Expect(gallery.WriteSidecar(dir, "tiny", &gallery.ModelConfig{Name: "tiny", Version: gallery.SidecarVersion,
			Origin:  &gallery.Origin{EntryName: "tiny", EntryDigest: "sha256:b"},
			Upgrade: &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseConfirmed, RetainedFiles: []string{"tiny.gguf.prev-12345678"}}})).To(Succeed())
	})

	It("sets and clears the hold", func() {
		w := do(http.MethodPut, "/api/models/tiny/hold", `{"hold":true}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var resp schema.ModelHoldResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(Equal(schema.ModelHoldResponse{Name: "tiny", Hold: true}))
		sc, err := gallery.ReadSidecar(dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.Hold).To(BeTrue())
		Expect(do(http.MethodPut, "/api/models/none/hold", `{"hold":true}`).Code).To(Equal(http.StatusNotFound))
	})

	It("removes the old version once, then answers 409", func() {
		w := do(http.MethodPost, "/api/models/tiny/remove-old", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var resp schema.ModelRemoveOldResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Removed).To(Equal([]string{"tiny.gguf.prev-12345678"}))
		Expect(do(http.MethodPost, "/api/models/tiny/remove-old", "").Code).To(Equal(http.StatusConflict))
	})

	It("tells what stayed when it removed only some of the old files", func() {
		Expect(os.WriteFile(filepath.Join(dir, "shared.gguf.prev-12345678"), []byte("s"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("name: other\nparameters:\n  model: shared.gguf.prev-12345678\n"), 0600)).To(Succeed())
		Expect(gallery.WriteSidecar(dir, "tiny", &gallery.ModelConfig{Name: "tiny", Version: gallery.SidecarVersion,
			Origin: &gallery.Origin{EntryName: "tiny", EntryDigest: "sha256:b"},
			Upgrade: &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseConfirmed,
				RetainedFiles: []string{"tiny.gguf.prev-12345678", "shared.gguf.prev-12345678"}}})).To(Succeed())
		w := do(http.MethodPost, "/api/models/tiny/remove-old", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var resp schema.ModelRemoveOldResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Removed).To(Equal([]string{"tiny.gguf.prev-12345678"}))
		Expect(resp.Kept).To(Equal([]string{"shared.gguf.prev-12345678"}))
		Expect(resp.Errors).To(ConsistOf(ContainSubstring("used by other")))
	})

	It("answers a file that stayed with 409, and an unexpected error without its text", func() {
		Expect(os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("name: other\nparameters:\n  model: tiny.gguf.prev-12345678\n"), 0600)).To(Succeed())
		Expect(do(http.MethodPost, "/api/models/tiny/remove-old", "").Code).To(Equal(http.StatusConflict))

		Expect(os.Remove(filepath.Join(dir, "other.yaml"))).To(Succeed())
		Expect(os.WriteFile(gallery.SidecarPath(dir, "tiny"), []byte("version: [unclosed\n"), 0600)).To(Succeed())
		w := do(http.MethodPost, "/api/models/tiny/remove-old", "")
		Expect(w.Code).To(Equal(http.StatusInternalServerError))
		Expect(w.Body.String()).ToNot(ContainSubstring(dir))
	})
})

var _ = Describe("The removed POST /api/models/config/{id}", func() {
	It("answers 410 Gone and points at the upgrade route", func() {
		e := echo.New()
		e.POST("/api/models/config/:id", RemovedModelConfigEndpoint())
		req := httptest.NewRequest(http.MethodPost, "/api/models/config/localai%40tiny", nil)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusGone))
		Expect(w.Header().Get("Link")).To(Equal(`</api/models/upgrade/{name}>; rel="successor-version"`))
		var body schema.RemovedRouteResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Error).To(ContainSubstring("removed"))
		Expect(body.Replacement).To(Equal("POST /api/models/upgrade/{name}"))
	})
})

var _ = Describe("The adopt route", func() {
	var (
		e   *echo.Echo
		dir string
	)

	do := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w
	}
	entry := func(galleryName string) *gallery.GalleryModel {
		return &gallery.GalleryModel{
			Metadata:  gallery.Metadata{Name: "tiny", Gallery: config.Gallery{Name: galleryName}},
			Overrides: map[string]any{"backend": "llama-cpp", "parameters": map[string]any{"model": "tiny.gguf"}},
		}
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		listing := []*gallery.GalleryModel{entry("a"), entry("b")}
		up := &modelupdates.Upgrader{SystemState: state, ListGallery: func(context.Context, bool) ([]*gallery.GalleryModel, error) { return listing, nil }}
		e = echo.New()
		e.POST("/api/models/:name/adopt", AdoptModelEndpoint(ModelUpgradeDeps{Upgrader: func() *modelupdates.Upgrader { return up }}))
		Expect(os.WriteFile(filepath.Join(dir, "tiny.yaml"), []byte("name: tiny\nbackend: llama-cpp\n"), 0600)).To(Succeed())
		Expect(gallery.WriteSidecar(dir, "tiny", &gallery.ModelConfig{Name: "tiny"})).To(Succeed())
	})

	It("lists the candidates, adopts from the gallery named, then answers 409", func() {
		w := do("/api/models/tiny/adopt", "")
		Expect(w.Code).To(Equal(http.StatusConflict), w.Body.String())
		var refused schema.ModelUpgradeError
		Expect(json.Unmarshal(w.Body.Bytes(), &refused)).To(Succeed())
		Expect(refused.Candidates).To(ConsistOf("a@tiny", "b@tiny"))

		w = do("/api/models/tiny/adopt", `{"gallery":"b"}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var resp schema.ModelAdoptResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).To(Equal(schema.ModelAdoptResponse{Name: "tiny", Gallery: "b", Entry: "tiny", FilesMatch: true}))
		Expect(os.ReadFile(filepath.Join(dir, "tiny.yaml"))).To(Equal([]byte("name: tiny\nbackend: llama-cpp\n")))

		Expect(do("/api/models/tiny/adopt", `{"gallery":"b"}`).Code).To(Equal(http.StatusConflict))
	})

	It("answers 422 with the candidates when the gallery named does not list it, and 404 when not installed", func() {
		w := do("/api/models/tiny/adopt", `{"gallery":"c"}`)
		Expect(w.Code).To(Equal(http.StatusUnprocessableEntity))
		var refused schema.ModelUpgradeError
		Expect(json.Unmarshal(w.Body.Bytes(), &refused)).To(Succeed())
		Expect(refused.Candidates).To(ConsistOf("a@tiny", "b@tiny"))
		Expect(do("/api/models/none/adopt", "").Code).To(Equal(http.StatusNotFound))
	})
})
