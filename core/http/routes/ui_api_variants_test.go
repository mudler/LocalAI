package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin the cost contract of the gallery listing.
//
// Variant description probes every referenced entry's weight files over the
// network. Doing that inline in the listing makes one page load cost
// (entries x variants) serial round trips, so the listing must not do it at
// all: it reports only the cheap `has_variants` flag, and a client that wants
// the description asks for one entry at a time.
//
// The probe counter below is what makes that a behavioral assertion rather
// than a structural one. It counts real HTTP hits on the weight files, so it
// goes red if DescribeVariants becomes reachable from the listing path again,
// through any caller.
var _ = Describe("Model gallery variants API", func() {
	var (
		app          *echo.Echo
		modelsDir    string
		weightServer *httptest.Server
		indexServer  *httptest.Server
		probes       *atomic.Int64
		appConfig    *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		modelsDir, err = os.MkdirTemp("", "ui-api-variants-test-*")
		Expect(err).NotTo(HaveOccurred())

		probes = &atomic.Int64{}
		// Stands in for the weight files a variant probe would range-fetch.
		// Every hit is a probe; a listing that describes variants inline
		// cannot avoid them.
		weightServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			probes.Add(1)
			w.Header().Set("Content-Length", "1048576")
			w.WriteHeader(http.StatusOK)
		}))

		index := fmt.Sprintf(`
- name: base-entry
  description: An entry that declares variants
  backend: llama-cpp
  files:
    - filename: base.gguf
      uri: %s/base.gguf
      sha256: ""
  variants:
    - model: big-entry
- name: big-entry
  description: The alternative build
  backend: llama-cpp
  files:
    - filename: big.gguf
      uri: %s/big.gguf
      sha256: ""
- name: plain-entry
  description: An entry that declares nothing
  backend: whisper
`, weightServer.URL, weightServer.URL)

		indexServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(index))
		}))

		systemState, err := system.GetSystemState(system.WithModelPath(modelsDir))
		Expect(err).NotTo(HaveOccurred())

		appConfig = &config.ApplicationConfig{
			Galleries:   []config.Gallery{{Name: "test", URL: indexServer.URL + "/index.yaml"}},
			SystemState: systemState,
		}

		galleryService := galleryop.NewGalleryService(appConfig, nil)
		app = echo.New()
		routes.RegisterUIAPIRoutes(app, config.NewModelConfigLoader(modelsDir), model.NewModelLoader(systemState), appConfig,
			galleryService, galleryop.NewOpCache(galleryService), &application.Application{},
			func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	})

	AfterEach(func() {
		weightServer.Close()
		indexServer.Close()
		Expect(os.RemoveAll(modelsDir)).To(Succeed())
	})

	get := func(path string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		return rec.Code, body
	}

	listing := func() []map[string]any {
		code, body := get("/api/models?items=9999")
		Expect(code).To(Equal(http.StatusOK))
		raw, ok := body["models"].([]any)
		Expect(ok).To(BeTrue(), "listing must return a models array")
		out := make([]map[string]any, 0, len(raw))
		for _, m := range raw {
			out = append(out, m.(map[string]any))
		}
		return out
	}

	find := func(models []map[string]any, name string) map[string]any {
		for _, m := range models {
			if m["name"] == name {
				return m
			}
		}
		Fail("no entry named " + name + " in the listing")
		return nil
	}

	Context("the listing", func() {
		It("issues no variant probes at all", func() {
			models := listing()
			Expect(models).NotTo(BeEmpty())

			// The whole point. An entry declaring variants must cost the
			// listing exactly what an entry declaring none costs it.
			Expect(probes.Load()).To(BeZero(),
				"the gallery listing probed variant weight files; variant description must not be reachable from the listing path")
		})

		It("omits the described variant payload", func() {
			entry := find(listing(), "base-entry")
			Expect(entry).NotTo(HaveKey("variants"))
			Expect(entry).NotTo(HaveKey("auto_variant"))
		})

		It("reports has_variants so a client knows whether to ask", func() {
			models := listing()
			Expect(find(models, "base-entry")["has_variants"]).To(BeTrue())
			// An entry declaring nothing must look exactly as it did before
			// variants existed, so a client never asks about it.
			Expect(find(models, "plain-entry")).NotTo(HaveKey("has_variants"))
		})
	})

	Context("the companion endpoint", func() {
		It("returns the description the listing used to carry", func() {
			code, body := get("/api/models/variants/test@base-entry")
			Expect(code).To(Equal(http.StatusOK))

			Expect(body).To(HaveKey("auto_selected"))
			variants, ok := body["variants"].([]any)
			Expect(ok).To(BeTrue())
			Expect(variants).To(HaveLen(2), "the declared variant plus the base")

			byModel := map[string]map[string]any{}
			for _, v := range variants {
				vm := v.(map[string]any)
				byModel[vm["model"].(string)] = vm
			}
			Expect(byModel).To(HaveKey("big-entry"))
			Expect(byModel).To(HaveKey("base-entry"))
			Expect(byModel["base-entry"]["is_base"]).To(BeTrue())
			Expect(byModel["big-entry"]["is_base"]).To(BeFalse())
			Expect(byModel["big-entry"]).To(HaveKey("backend"))
			Expect(byModel["big-entry"]).To(HaveKey("fits"))
		})

		It("omits memory_bytes entirely when a size cannot be determined", func() {
			// The weight server answers without a usable size, so the probe
			// comes back unknown. An absent key is the contract: a zero would
			// read as 'needs nothing'.
			_, body := get("/api/models/variants/test@base-entry")
			for _, v := range body["variants"].([]any) {
				vm := v.(map[string]any)
				if mb, present := vm["memory_bytes"]; present {
					Expect(mb).NotTo(BeZero(), "memory_bytes must be omitted rather than serialized as zero")
				}
			}
		})

		It("returns an empty description for an entry declaring no variants", func() {
			code, body := get("/api/models/variants/test@plain-entry")
			Expect(code).To(Equal(http.StatusOK))
			Expect(body["variants"]).To(BeEmpty())
			Expect(body["auto_selected"]).To(BeEmpty())
		})

		It("404s an unknown entry", func() {
			code, _ := get("/api/models/variants/test@nope")
			Expect(code).To(Equal(http.StatusNotFound))
		})
	})
})
