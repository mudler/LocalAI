package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
)

// fakeLocalAI is a minimal HTTP server that mimics the relevant LocalAI
// admin endpoints. Only the routes the httpapi.Client touches need to exist.
func fakeLocalAI() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/models/available", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"name":        "qwen2.5-7b-instruct",
				"description": "Qwen 2.5 chat",
				"license":     "apache-2.0",
				"tags":        []string{"chat", "tools"},
				"gallery":     map[string]any{"name": "official", "url": "http://gallery"},
				"installed":   false,
			},
			{
				"name":    "stable-diffusion-3.5",
				"tags":    []string{"image"},
				"gallery": map[string]any{"name": "official", "url": "http://gallery"},
			},
		})
	})

	mux.HandleFunc("/models/galleries", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "official", "url": "http://gallery"},
		})
	})

	mux.HandleFunc("/models/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":   "job-123",
			"status": r.Host + "/models/jobs/job-123",
		})
	})

	mux.HandleFunc("/models/jobs/job-123", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"processed": true, "progress": 100.0, "message": "done",
		})
	})

	mux.HandleFunc("/models/jobs/missing", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "could not find any status for ID", http.StatusInternalServerError)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		// LocalAI's welcome JSON.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Version":      "v0.0.0-test",
			"LoadedModels": []any{},
			"InstalledBackends": map[string]bool{
				"llama-cpp": true,
				"whisper":   true,
			},
			"ModelsConfig": []map[string]any{
				{"name": "qwen2.5-7b-instruct", "backend": "llama-cpp"},
			},
		})
	})

	mux.HandleFunc("/backends", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "llama-cpp", "installed": true, "tags": []string{"chat"}},
		})
	})

	return httptest.NewServer(mux)
}

var _ = Describe("httpapi.Client against the LocalAI admin REST surface", func() {
	var (
		srv *httptest.Server
		c   *Client
		ctx context.Context
	)

	BeforeEach(func() {
		srv = fakeLocalAI()
		c = New(srv.URL, "")
		ctx = context.Background()
	})

	AfterEach(func() {
		srv.Close()
	})

	Describe("GallerySearch", func() {
		It("filters by tag, applies limit, and preserves tags on the result", func() {
			hits, err := c.GallerySearch(ctx, localaitools.GallerySearchQuery{Query: "qwen", Tag: "chat", Limit: 5})
			Expect(err).ToNot(HaveOccurred())
			Expect(hits).To(HaveLen(1))
			Expect(hits[0].Name).To(Equal("qwen2.5-7b-instruct"))
			Expect(containsTagExact(hits[0].Tags, "chat")).To(BeTrue())
		})
	})

	Describe("ListGalleries", func() {
		It("returns the configured galleries", func() {
			out, err := c.ListGalleries(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(HaveLen(1))
			Expect(out[0].Name).To(Equal("official"))
		})
	})

	Describe("InstallModel", func() {
		It("returns the job id parsed from the apply response", func() {
			id, err := c.InstallModel(ctx, localaitools.InstallModelRequest{ModelName: "qwen2.5-7b-instruct"})
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal("job-123"))
		})

		It("forwards a chosen variant to the apply endpoint", func() {
			// The assistant's whole ability to honor "install the Q8 one"
			// rests on this field surviving the hop to REST.
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				_ = json.NewEncoder(w).Encode(map[string]any{"uuid": "job-123"})
			}))
			DeferCleanup(srv.Close)

			_, err := New(srv.URL, "").InstallModel(context.Background(), localaitools.InstallModelRequest{
				ModelName: "qwen2.5-7b-instruct",
				Variant:   "qwen2.5-7b-instruct-q8",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(body).To(HaveKeyWithValue("variant", "qwen2.5-7b-instruct-q8"))
		})

		It("omits the variant when none was chosen, so the server auto-selects", func() {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				_ = json.NewEncoder(w).Encode(map[string]any{"uuid": "job-123"})
			}))
			DeferCleanup(srv.Close)

			_, err := New(srv.URL, "").InstallModel(context.Background(), localaitools.InstallModelRequest{
				ModelName: "qwen2.5-7b-instruct",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(body).ToNot(HaveKey("variant"))
		})
	})

	Describe("GetJobStatus", func() {
		It("decodes the happy-path response", func() {
			st, err := c.GetJobStatus(ctx, "job-123")
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Processed).To(BeTrue())
			Expect(st.Progress).To(Equal(100.0))
		})

		It("translates the legacy 500-with-could-not-find as nil status, nil err", func() {
			st, err := c.GetJobStatus(ctx, "missing")
			Expect(err).ToNot(HaveOccurred(), "legacy 500 should not surface as a real error")
			Expect(st).To(BeNil())
		})
	})

	Describe("SystemInfo", func() {
		It("extracts version and installed-backend names from the welcome JSON", func() {
			info, err := c.SystemInfo(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Version).To(Equal("v0.0.0-test"))
			Expect(info.InstalledBackends).To(HaveLen(2))
		})
	})

	Describe("ListBackends", func() {
		It("returns each installed backend with its installed flag", func() {
			bs, err := c.ListBackends(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(bs).To(HaveLen(1))
			Expect(bs[0].Name).To(Equal("llama-cpp"))
			Expect(bs[0].Installed).To(BeTrue())
		})
	})
})

var _ = Describe("Model aliases", func() {
	Describe("ListAliases", func() {
		It("passes the GET /api/aliases payload through unchanged", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/api/aliases"))
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"name": "gpt-4", "target": "qwen"},
				})
			}))
			DeferCleanup(srv.Close)

			out, err := New(srv.URL, "").ListAliases(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(HaveLen(1))
			Expect(out[0].Name).To(Equal("gpt-4"))
			Expect(out[0].Target).To(Equal("qwen"))
		})
	})

	Describe("SetAlias", func() {
		It("swaps an existing alias via PATCH without falling back to import", func() {
			var patched, imported bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPatch && r.URL.Path == "/api/models/config-json/gpt-4":
					patched = true
					var body map[string]any
					Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
					Expect(body).To(HaveKeyWithValue("alias", "qwen"))
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
				case r.URL.Path == "/models/import":
					imported = true
					w.WriteHeader(http.StatusOK)
				default:
					http.Error(w, "unexpected", http.StatusTeapot)
				}
			}))
			DeferCleanup(srv.Close)

			Expect(New(srv.URL, "").SetAlias(context.Background(), "gpt-4", "qwen")).To(Succeed())
			Expect(patched).To(BeTrue(), "PATCH should be attempted first")
			Expect(imported).To(BeFalse(), "import must not run when PATCH succeeds")
		})

		It("creates a fresh alias via import when PATCH reports the model is missing", func() {
			var imported bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPatch:
					http.Error(w, "model configuration not found", http.StatusNotFound)
				case r.Method == http.MethodPost && r.URL.Path == "/models/import":
					imported = true
					var body map[string]any
					Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
					Expect(body).To(HaveKeyWithValue("name", "gpt-4"))
					Expect(body).To(HaveKeyWithValue("alias", "qwen"))
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
				default:
					http.Error(w, "unexpected", http.StatusTeapot)
				}
			}))
			DeferCleanup(srv.Close)

			Expect(New(srv.URL, "").SetAlias(context.Background(), "gpt-4", "qwen")).To(Succeed())
			Expect(imported).To(BeTrue(), "import should create the alias on a 404")
		})

		It("surfaces a non-404 PATCH error without attempting import", func() {
			var imported bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models/import" {
					imported = true
				}
				http.Error(w, "target is an alias", http.StatusBadRequest)
			}))
			DeferCleanup(srv.Close)

			err := New(srv.URL, "").SetAlias(context.Background(), "gpt-4", "bad")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("target is an alias"))
			Expect(imported).To(BeFalse(), "a 400 swap error must not trigger create")
		})
	})
})

var _ = Describe("ErrHTTPNotFound", func() {
	Context("on a clean 404 status", func() {
		var (
			srv *httptest.Server
			c   *Client
		)
		BeforeEach(func() {
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "nope", http.StatusNotFound)
			}))
			c = New(srv.URL, "")
		})
		AfterEach(func() { srv.Close() })

		It("translates a 404 on /models/jobs/:id into nil status, nil err", func() {
			st, err := c.GetJobStatus(context.Background(), "missing")
			Expect(err).ToNot(HaveOccurred())
			Expect(st).To(BeNil())
		})

		It("is detectable via errors.Is when callers don't translate", func() {
			_, err := c.ListGalleries(context.Background())
			Expect(errors.Is(err, ErrHTTPNotFound)).To(BeTrue(), "got: %v", err)
		})
	})

	Context("on the legacy 500-with-could-not-find body", func() {
		It("treats it as not-found until LocalAI returns a proper 404", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "could not find any status for ID", http.StatusInternalServerError)
			}))
			DeferCleanup(srv.Close)
			c := New(srv.URL, "")
			st, err := c.GetJobStatus(context.Background(), "missing")
			Expect(err).ToNot(HaveOccurred())
			Expect(st).To(BeNil())
		})
	})
})

var _ = Describe("Bearer token", func() {
	It("forwards the configured API key on every request", func() {
		var sawAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sawAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		}))
		DeferCleanup(srv.Close)

		c := New(srv.URL, "secret-key")
		_, err := c.ListGalleries(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(sawAuth).To(Equal("Bearer secret-key"))
	})
})
