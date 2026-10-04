// SPDX-License-Identifier: MIT
package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	localaiapp "github.com/mudler/LocalAI/core/application"
	httpapi "github.com/mudler/LocalAI/core/http"
	"io"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// Installation consumes the actual committed gallery, with cached artifacts
// checked BEFORE invoking its downloader. Missing/mismatched files fail closed.
func installRealDecisionGallery(modelsPath string) {
	cache := os.Getenv("DECISION_MODEL_CACHE")
	Expect(cache).NotTo(BeEmpty())
	data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "index.yaml"))
	Expect(err).NotTo(HaveOccurred())
	var entries []gallery.GalleryModel
	Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
	var entry gallery.GalleryModel
	for _, e := range entries {
		if e.Name == "openjev-llama-cpp" {
			entry = e
			break
		}
	}
	Expect(entry.Name).To(Equal("openjev-llama-cpp"))
	Expect(entry.AdditionalFiles).To(HaveLen(2))
	for _, f := range entry.AdditionalFiles {
		src := filepath.Join(cache, f.Filename)
		file, err := os.Open(src)
		Expect(err).NotTo(HaveOccurred())
		h := sha256.New()
		n, err := io.Copy(h, file)
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Close()).To(Succeed())
		Expect(fmt.Sprintf("%x", h.Sum(nil))).To(Equal(f.SHA256))
		Expect(os.Symlink(src, filepath.Join(modelsPath, f.Filename))).To(Succeed())
		_, _ = fmt.Fprintf(GinkgoWriter, "CACHED GALLERY ARTIFACT %s bytes=%d sha256=%s\n", f.Filename, n, f.SHA256)
	}
	state, err := system.GetSystemState(system.WithModelPath(modelsPath))
	Expect(err).NotTo(HaveOccurred())
	catalog := filepath.Join(modelsPath, "gallery-catalog.catalog")
	Expect(os.WriteFile(catalog, data, 0600)).To(Succeed())
	err = gallery.InstallModelFromGallery(context.Background(), []config.Gallery{{Name: "committed", URL: "file://" + catalog}}, nil, state, nil, "committed@openjev-llama-cpp", gallery.GalleryModel{}, func(_, _, _ string, _ float64) {}, false, false, false)
	Expect(err).NotTo(HaveOccurred())
	cfgPath := filepath.Join(modelsPath, "openjev-llama-cpp.yaml")
	installed, err := os.ReadFile(cfgPath)
	Expect(err).NotTo(HaveOccurred())
	var cfg map[string]any
	Expect(yaml.Unmarshal(installed, &cfg)).To(Succeed())
	Expect(cfg["mmproj"]).To(Equal("mmproj-OpenJev-Q8_0.gguf"))
	Expect(cfg["context_size"]).To(Equal(8192))
	// Only resource controls change after the genuine gallery installation.
	cfg["threads"] = 4
	cfg["gpu_layers"] = 0
	cfg["batch"] = 512
	cfg["options"] = []string{"parallel:1"}
	cfg["name"] = "openjev-llama-cpp"
	writeDecisionConfigAt(modelsPath, cfg)
	writeDecisionConfigAt(modelsPath, decisionRouterConfig("mm-real-router", "decisions", "openjev-llama-cpp"))
	// Final generation remains explicitly mocked; only decisions load real weights.
	for _, name := range []string{"mm-red", "mm-blue"} {
		writeDecisionConfigAt(modelsPath, map[string]any{"name": name, "backend": "mock-backend", "known_usecases": []string{"chat", "vision"}, "parameters": map[string]any{"model": name + ".bin"}})
	}
}

var _ = Describe("Gallery multimodal public API", Label("MultimodalReal", "real-models"), func() {
	It("installs cached OpenJev and distinguishes images through SystemOne and both routers", func() {
		if os.Getenv("DECISION_REAL_E2E") != "1" {
			Skip("set DECISION_REAL_E2E=1 with cached artifacts and CPU backend")
		}
		modelsPath := decisionIsolatedPath()
		installRealDecisionGallery(modelsPath)
		binary := os.Getenv("DECISION_BACKEND")
		Expect(binary).NotTo(BeEmpty())
		realApp, realURL, cleanup := decisionIsolatedApp(modelsPath, binary)
		defer cleanup()
		for _, blue := range []bool{false, true} {
			want := "red"
			if blue {
				want = "blue"
			}
			code, data := decisionPostAt(realURL, "/systemone", map[string]any{"model": "openjev-llama-cpp", "state": map[string]any{}, "images": []string{decisionImage(blue)}, "questions": map[string]any{"color": map[string]any{"type": "choice", "instructions": "What is the dominant color of the image?", "criteria": map[string]any{"red": nil, "blue": nil}}}})
			Expect(code).To(Equal(200), string(data))
			var response schema.SystemOneResponse
			Expect(json.Unmarshal(data, &response)).To(Succeed())
			a := response.Answers["color"]
			Expect(a.Choice).NotTo(BeNil())
			Expect(*a.Choice).To(Equal(want))
			Expect(a.Probabilities).To(HaveLen(2))
			sum := 0.0
			for _, v := range a.Probabilities {
				Expect(math.IsNaN(v) || math.IsInf(v, 0)).To(BeFalse())
				Expect(v).To(BeNumerically(">=", 0))
				sum += v
			}
			Expect(sum).To(BeNumerically("~", 1, 0.0001))
			Expect(a.Probabilities[want]).To(BeNumerically(">", 0.9))
			_, _ = fmt.Fprintf(GinkgoWriter, "REAL SYSTEMONE %s %s\n", want, data)
			for _, anthropic := range []bool{false, true} {
				endpoint := "/chat/completions"
				if anthropic {
					endpoint = "/messages"
				}
				code, data = decisionPostAt(realURL, endpoint, imageChat("mm-real-router", []string{decisionImage(blue)}, anthropic))
				Expect(code).To(Equal(200), string(data))
				rows, err := realApp.RouterDecisions().List(context.Background(), router.DecisionListQuery{RouterModel: "mm-real-router", Limit: 1})
				Expect(err).NotTo(HaveOccurred())
				Expect(rows).To(HaveLen(1))
				d := rows[0]
				Expect(d.Classifier).To(Equal("decisions"))
				Expect(d.ServedModel).To(Equal("mm-" + want))
				Expect(d.Label).To(Equal(want))
				Expect(d.Cached).To(BeFalse())
				Expect(d.LabelScores).To(HaveLen(2))
				_, _ = fmt.Fprintf(GinkgoWriter, "REAL ROUTER endpoint=%s image=%s decision=%+v\n", endpoint, want, d)
			}
		}
	})
})

// Each fixture owns its loader, model namespace, HTTP server and processes.
// In particular, no shared llama-cpp mapping or loaded candidate is reused.
func decisionIsolatedPath() string {
	dir, err := os.MkdirTemp(tmpDir, "decision-isolated-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
	return dir
}
func decisionIsolatedApp(dir, binary string) (*localaiapp.Application, string, func()) {
	state, err := system.GetSystemState(system.WithModelPath(dir), system.WithBackendPath(filepath.Join(dir, "backends")))
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	a, err := localaiapp.New(config.WithContext(ctx), config.WithSystemState(state), config.WithGeneratedContentDir(filepath.Join(dir, "generated")))
	Expect(err).NotTo(HaveOccurred())
	a.ModelLoader().SetExternalBackend("llama-cpp", binary)
	a.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)
	app, err := httpapi.API(a)
	Expect(err).NotTo(HaveOccurred())
	server := httptest.NewServer(app)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			server.Close()
			defer cancel()
			Expect(a.Shutdown()).To(Succeed())
		})
	}
	DeferCleanup(cleanup)
	return a, server.URL + "/v1", cleanup
}

var _ = Describe("Decision fixture isolation", Label("Multimodal"), func() {
	It("keeps shared routing intact before, during and after an isolated app", func() {
		checkShared := func() {
			code, data := decisionPost("/chat/completions", imageChat("mm-router", []string{decisionImage(true)}, false))
			Expect(code).To(Equal(200), string(data))
			Expect(lastDecision("mm-router").ServedModel).To(Equal("mm-blue"))
		}
		checkShared()
		original := localAIApp.ModelLoader().GetExternalBackend("llama-cpp")
		red, err := os.ReadFile(filepath.Join(modelsPath, "mm-red.yaml"))
		Expect(err).NotTo(HaveOccurred())
		dir := decisionIsolatedPath()
		// Deliberately reuse model names with different settings across applications.
		writeDecisionConfigAt(dir, map[string]any{"name": "mm-decision", "backend": "llama-cpp", "known_usecases": []string{"decisions"}, "parameters": map[string]any{"model": "mm-error.bin"}})
		_, url, cleanup := decisionIsolatedApp(dir, mockBackendPath)
		code, _ := decisionPostAt(url, "/systemone", map[string]any{"model": "mm-decision", "state": map[string]any{}, "questions": map[string]any{"q": map[string]any{"type": "noul"}}})
		Expect(code).To(Equal(500))
		checkShared()
		cleanup()
		checkShared()
		Expect(localAIApp.ModelLoader().GetExternalBackend("llama-cpp")).To(Equal(original))
		after, err := os.ReadFile(filepath.Join(modelsPath, "mm-red.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(red))
	})
})
