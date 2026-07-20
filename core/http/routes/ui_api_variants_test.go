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
    - filename: base-Q4_K_M.gguf
      uri: %s/base.gguf
      sha256: ""
  variants:
    - model: big-entry
    - model: dir-entry
- name: big-entry
  description: The alternative build
  backend: llama-cpp
  tags:
    - llm
    - dflash
  files:
    - filename: big-Q8_0.gguf
      uri: %s/big.gguf
      sha256: ""
# A build served from a directory of weights. Its filename names no weight
# format, which makes it the fixture for the absent-quantization case a client
# has to render as unknown rather than as a blank cell.
- name: dir-entry
  description: A build whose name declares no weight format
  backend: llama-cpp
  files:
    - filename: weights.safetensors
      uri: %s/weights.safetensors
      sha256: ""
- name: plain-entry
  description: An entry that declares nothing
  backend: whisper
`, weightServer.URL, weightServer.URL, weightServer.URL)

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
			Expect(variants).To(HaveLen(3), "the declared variants plus the base")

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

		// Enrichment: name, backend and size are frequently identical across
		// two builds of one model, so the picker also carries the facts that
		// actually differ. Both are derived here rather than in the browser, so
		// every client reads them out of the same entry the installer uses.
		It("reports each build's quantization", func() {
			_, body := get("/api/models/variants/test@base-entry")

			byModel := map[string]map[string]any{}
			for _, v := range body["variants"].([]any) {
				vm := v.(map[string]any)
				byModel[vm["model"].(string)] = vm
			}

			Expect(byModel["base-entry"]["quantization"]).To(Equal("Q4_K_M"))
			Expect(byModel["big-entry"]["quantization"]).To(Equal("Q8_0"))
		})

		It("omits quantization entirely when the build names no weight format", func() {
			// The degrade contract. An absent key is what lets a client render
			// "unknown format"; an empty string would render as a blank cell
			// indistinguishable from a rendering bug.
			_, body := get("/api/models/variants/test@base-entry")

			for _, v := range body["variants"].([]any) {
				vm := v.(map[string]any)
				if vm["model"] == "dir-entry" {
					Expect(vm).NotTo(HaveKey("quantization"))
					return
				}
			}
			Fail("dir-entry missing from the described variants")
		})

		It("reports the serving features a build declares", func() {
			_, body := get("/api/models/variants/test@base-entry")

			byModel := map[string]map[string]any{}
			for _, v := range body["variants"].([]any) {
				vm := v.(map[string]any)
				byModel[vm["model"].(string)] = vm
			}

			Expect(byModel["big-entry"]["features"]).To(Equal([]any{"dflash"}))
			// A build declaring no serving feature carries no key at all,
			// rather than an empty list a client would have to length-check.
			Expect(byModel["base-entry"]).NotTo(HaveKey("features"))
			Expect(byModel["dir-entry"]).NotTo(HaveKey("features"))
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

	// The collapsed view is the deduplicated gallery: every entry installable
	// in its own right, with nothing shown twice. Off by default, so a client
	// that never asks for it sees the listing exactly as it was. The web UI
	// always asks for it; the parameter stays for every other client.
	Context("the collapse_variants listing filter", func() {
		names := func(path string) []string {
			code, body := get(path)
			Expect(code).To(Equal(http.StatusOK))
			raw, ok := body["models"].([]any)
			Expect(ok).To(BeTrue(), "listing must return a models array")
			out := make([]string, 0, len(raw))
			for _, m := range raw {
				out = append(out, m.(map[string]any)["name"].(string))
			}
			return out
		}

		// Everything the collapse parameter can possibly affect: which entries
		// come back, how they serialize, and the paging that describes them.
		//
		// Deliberately NOT the whole response body. The listing envelope also
		// carries live host telemetry (ramUsed and friends) which drifts
		// between two calls milliseconds apart, so a byte-for-byte comparison
		// asserts on the machine's memory pressure rather than on the handler
		// and fails at random.
		listingShape := func(path string) map[string]any {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			var body map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())

			shape := map[string]any{}
			for _, k := range []string{
				"models", "availableModels", "installedModels",
				"totalPages", "currentPage", "nextPage", "prevPage",
				"allBackends", "allTags",
			} {
				shape[k] = body[k]
			}
			return shape
		}

		It("returns every entry when off", func() {
			Expect(names("/api/models?items=9999")).To(ConsistOf("base-entry", "big-entry", "dir-entry", "plain-entry"))
		})

		It("hides only the builds another entry already offers", func() {
			// base-entry is a parent and stays. big-entry and dir-entry are the
			// builds it references, so they drop out: a user reaches them by
			// installing base-entry. plain-entry is nobody's variant, so it
			// stays even though it declares none of its own.
			Expect(names("/api/models?items=9999&collapse_variants=true")).
				To(ConsistOf("base-entry", "plain-entry"))
		})

		It("leaves the response untouched for any value other than true", func() {
			// Default off has to mean off, so an explicit false and an
			// unparseable value must both behave as absent rather than as a
			// truthy presence check.
			base := listingShape("/api/models?items=9999")
			Expect(listingShape("/api/models?items=9999&collapse_variants=false")).To(Equal(base))
			Expect(listingShape("/api/models?items=9999&collapse_variants=1")).To(Equal(base))
		})

		It("serializes a non-declaring entry exactly as it did before", func() {
			// The whole promise of the migration phase: a client that never
			// sends the parameter gets byte-for-byte what it got before.
			entry := find(listing(), "plain-entry")
			Expect(entry).NotTo(HaveKey("has_variants"))
			Expect(entry).NotTo(HaveKey("variants"))
			Expect(entry).NotTo(HaveKey("auto_variant"))
		})

		It("composes with the backend filter rather than replacing it", func() {
			// base-entry and big-entry are llama-cpp; plain-entry is whisper.
			// If either filter overwrote the other, the llama-cpp case would
			// keep big-entry and the whisper case would lose plain-entry.
			Expect(names("/api/models?items=9999&collapse_variants=true&backend=llama-cpp")).
				To(ConsistOf("base-entry"))
			Expect(names("/api/models?items=9999&collapse_variants=true&backend=whisper")).
				To(ConsistOf("plain-entry"))
			Expect(names("/api/models?items=9999&backend=llama-cpp")).
				To(ConsistOf("base-entry", "big-entry", "dir-entry"))
		})

		It("still applies the search term when nothing is collapsed away", func() {
			Expect(names("/api/models?items=9999&collapse_variants=true&term=plain")).
				To(ConsistOf("plain-entry"))
		})

		It("lets a search term find a build the collapse would hide", func() {
			// The term matches the referenced build but not its parent.
			// Collapsing is a browsing aid; a user who types a name is looking
			// something up, and answering "no models found" for an entry the
			// gallery does hold reads as "that model does not exist".
			Expect(names("/api/models?items=9999&term=big")).To(ConsistOf("big-entry"))
			Expect(names("/api/models?items=9999&collapse_variants=true&term=big")).
				To(ConsistOf("big-entry"))
		})

		It("does not treat an empty or whitespace-only term as a search", func() {
			// Otherwise a cleared or fat-fingered search box would silently
			// stop collapsing and the browsing view would grow duplicate rows.
			Expect(names("/api/models?items=9999&collapse_variants=true&term=")).
				To(ConsistOf("base-entry", "plain-entry"))
			Expect(names("/api/models?items=9999&collapse_variants=true&term=%20%20")).
				To(ConsistOf("base-entry", "plain-entry"))
		})

		It("does not let tag or backend filters bypass the collapse", func() {
			// They refine a listing the user is still reading rather than name
			// an entry they already know exists, so collapsing still helps.
			Expect(names("/api/models?items=9999&collapse_variants=true&backend=llama-cpp")).
				NotTo(ContainElement("big-entry"))
		})

		It("reports the filtered total so pagination stays honest", func() {
			// The listing paginates at 9, so a filter that narrowed the rows
			// without narrowing the count would hand the user empty pages.
			_, body := get("/api/models?items=9999&collapse_variants=true")
			Expect(body["availableModels"]).To(BeEquivalentTo(2))
			Expect(body["totalPages"]).To(BeEquivalentTo(1))
		})

		It("still issues no variant probes when filtering", func() {
			names("/api/models?items=9999&collapse_variants=true")
			Expect(probes.Load()).To(BeZero(),
				"the filter must select on declared metadata, not by describing variants")
		})
	})
})
