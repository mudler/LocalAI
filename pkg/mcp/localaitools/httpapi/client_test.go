package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
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

	mux.HandleFunc("/api/nodes/scheduling", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":                  "sched-1",
				"model_name":          "qwen",
				"min_replicas":        1,
				"max_replicas":        2,
				"unsatisfiable_ticks": 1,
				"unsatisfiable_until": "2026-01-01T00:00:00Z",
				"created_at":          "2026-01-01T00:00:00Z",
				"updated_at":          "2026-01-01T00:00:00Z",
			}})
		case http.MethodPost:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			body["id"] = "sched-1"
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/nodes/scheduling/qwen", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"model_name": "qwen", "spread_all": true})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
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

	Describe("Scheduling", func() {
		It("lists scheduling configs", func() {
			out, err := c.ListScheduling(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(HaveLen(1))
			Expect(out[0].ModelName).To(Equal("qwen"))
			Expect(out[0].MinReplicas).To(Equal(1))
			Expect(schedulingJSONKeys(&out[0])).ToNot(Or(
				HaveKey("id"),
				HaveKey("unsatisfiable_until"),
				HaveKey("unsatisfiable_ticks"),
				HaveKey("created_at"),
				HaveKey("updated_at"),
			))
		})

		It("gets one scheduling config", func() {
			out, err := c.GetScheduling(ctx, "qwen")
			Expect(err).ToNot(HaveOccurred())
			Expect(out.ModelName).To(Equal("qwen"))
			Expect(out.SpreadAll).To(BeTrue())
		})

		It("sets a scheduling config", func() {
			out, err := c.SetScheduling(ctx, localaitools.SetSchedulingRequest{ModelName: "qwen", MinReplicas: 1, MaxReplicas: 2})
			Expect(err).ToNot(HaveOccurred())
			Expect(out.ModelName).To(Equal("qwen"))
			Expect(out.MaxReplicas).To(Equal(2))
		})

		It("deletes a scheduling config", func() {
			Expect(c.DeleteScheduling(ctx, "qwen")).To(Succeed())
		})
	})
})

func schedulingJSONKeys(config *localaitools.ModelSchedulingConfig) map[string]any {
	var out map[string]any
	b, err := json.Marshal(config)
	Expect(err).ToNot(HaveOccurred())
	Expect(json.Unmarshal(b, &out)).To(Succeed())
	return out
}

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

var _ = Describe("Failover chains", func() {
	Describe("ListFailoverChains", func() {
		It("issues GET /api/failover and unwraps the chains array", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/api/failover"))
				_ = json.NewEncoder(w).Encode(map[string]any{
					"chains": []map[string]any{
						{
							"name":   "chain",
							"state":  "primary",
							"active": "a",
							"pinned": nil,
							"targets": []map[string]any{
								{"model": "a", "kind": "local", "warm": false, "state": "healthy"},
							},
						},
					},
				})
			}))
			DeferCleanup(srv.Close)

			out, err := New(srv.URL, "").ListFailoverChains(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(HaveLen(1))
			Expect(out[0].Name).To(Equal("chain"))
			Expect(out[0].Active).To(Equal("a"))
			Expect(out[0].Pinned).To(BeEmpty())
			Expect(out[0].Targets).To(ConsistOf(localaitools.FailoverTargetInfo{Model: "a", Kind: "local", State: "healthy"}))
		})
	})

	Describe("PinFailoverTarget", func() {
		It("issues POST /api/failover/:chain/pin with the target in the body", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.URL.Path).To(Equal("/api/failover/chain/pin"))
				var body map[string]string
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				Expect(body).To(HaveKeyWithValue("target", "b"))
				w.WriteHeader(http.StatusOK)
			}))
			DeferCleanup(srv.Close)

			Expect(New(srv.URL, "").PinFailoverTarget(context.Background(), "chain", "b")).To(Succeed())
		})
	})

	Describe("UnpinFailoverTarget", func() {
		It("issues DELETE /api/failover/:chain/pin", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodDelete))
				Expect(r.URL.Path).To(Equal("/api/failover/chain/pin"))
				w.WriteHeader(http.StatusOK)
			}))
			DeferCleanup(srv.Close)

			Expect(New(srv.URL, "").UnpinFailoverTarget(context.Background(), "chain")).To(Succeed())
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

var _ = Describe("InstallLibraryBackend", func() {
	It("posts the library entry to the backend library route", func() {
		var path string
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.Method + " " + r.URL.Path
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "installed", "name": "demo", "sourceId": "official"})
		}))
		DeferCleanup(srv.Close)

		res, err := New(srv.URL, "").InstallLibraryBackend(context.Background(), localaitools.InstallLibraryBackendRequest{SourceID: "official", Name: "demo", Version: "1.0.0"})
		Expect(err).ToNot(HaveOccurred())
		Expect(path).To(Equal("POST /backends/library/install"))
		Expect(body).To(Equal(map[string]any{"sourceId": "official", "name": "demo", "version": "1.0.0"}))
		Expect(res.Status).To(Equal("installed"))
	})
})

var _ = Describe("ListBackendLibraryUpdates", func() {
	It("reads the backend library updates route", func() {
		var path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.Method + " " + r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"updates": []map[string]any{
				{"name": "demo", "sourceId": "official", "installedVersion": "1.0.0", "version": "1.1.0", "digest": "sha256:aa", "signed": true},
			}})
		}))
		DeferCleanup(srv.Close)

		res, err := New(srv.URL, "").ListBackendLibraryUpdates(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(path).To(Equal("GET /backends/library/updates"))
		Expect(res).To(HaveLen(1))
		Expect(res[0].SourceID).To(Equal("official"))
		Expect(res[0].Version).To(Equal("1.1.0"))
	})
})

var _ = Describe("Shared folders", func() {
	// serve answers one request and checks its method, path and body.
	serve := func(method, path string, wantBody map[string]any, reply any) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Method).To(Equal(method))
			Expect(r.URL.EscapedPath()).To(Equal(path))
			if wantBody != nil {
				var body map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				Expect(body).To(Equal(wantBody))
			}
			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(reply)).To(Succeed())
		}))
		DeferCleanup(srv.Close)
		return srv
	}
	folder := localaitools.SharedFolder{ID: 3, Name: "Team", Path: "/shared/Team", Members: []schema.SharedFolderMember{{Principal: "@everyone", Name: "@everyone", Everyone: true, Mode: "read"}}}
	ctx := context.Background()

	It("lists the folders and the audit", func() {
		srv := serve(http.MethodGet, "/api/chats/admin/shared-folders", nil, map[string]any{"folders": []any{folder}})
		out, err := New(srv.URL, "").ListSharedFolders(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal([]localaitools.SharedFolder{folder}))

		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(Equal("/api/chats/admin/shared-folders/events"))
			Expect(r.URL.Query().Get("limit")).To(Equal("7"))
			_, _ = w.Write([]byte(`{"events":[{"actor":"root","action":"create","folder_id":3,"folder":"/shared/Team","at":"2026-10-30T09:00:00Z"}]}`))
		}))
		DeferCleanup(srv.Close)
		evs, err := New(srv.URL, "").ListSharedFolderEvents(ctx, 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(1))
		Expect(evs[0].Action).To(Equal("create"))
	})

	It("lists the built-in chat tools and the audit of the commands on this machine", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			switch r.URL.Path {
			case "/api/chats/admin/tools":
				_, _ = w.Write([]byte(`{"tools":[{"id":"bash","label":"Commands (bash)","setting":"chats_host_exec","enabled":true,"provider":"builtin","candidates":[],"mode":"ask"}],"skills":[{"id":"xlsx","provider":"office","candidates":["office"]}]}`))
			case "/api/chats/admin/host-exec/events":
				Expect(r.URL.Query().Get("limit")).To(Equal("3"))
				_, _ = w.Write([]byte(`{"events":[{"user":"alice","chat":"c1","command":"ls","exit":0,"duration_ms":4,"at":"2026-10-30T09:00:00Z"}]}`))
			default:
				Fail("unexpected path " + r.URL.Path)
			}
		}))
		DeferCleanup(srv.Close)
		list, err := New(srv.URL, "").ListChatTools(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list.Tools).To(HaveLen(1))
		Expect(list.Tools[0].Mode).To(Equal("ask"))
		Expect(list.Skills).To(HaveLen(1))
		Expect(list.Skills[0].Provider).To(Equal("office"))
		evs, err := New(srv.URL, "").ListHostExecEvents(ctx, 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(1))
		Expect(evs[0].Command).To(Equal("ls"))
	})

	It("creates, renames, removes and restores a folder", func() {
		out, err := New(serve(http.MethodPost, "/api/chats/admin/shared-folders", map[string]any{"name": "Team"}, folder).URL, "").CreateSharedFolder(ctx, "Team")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.ID).To(Equal(int64(3)))
		_, err = New(serve(http.MethodPatch, "/api/chats/admin/shared-folders/3", map[string]any{"name": "Crew"}, folder).URL, "").RenameSharedFolder(ctx, 3, "Crew")
		Expect(err).NotTo(HaveOccurred())
		removed, err := New(serve(http.MethodDelete, "/api/chats/admin/shared-folders/3", nil, localaitools.SharedFolderRemoved{Folder: folder, UndoRev: 42}).URL, "").DeleteSharedFolder(ctx, 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(removed.UndoRev).To(Equal(int64(42)))
		_, err = New(serve(http.MethodPost, "/api/chats/admin/shared-folders/restore", map[string]any{"rev": float64(42)}, folder).URL, "").RestoreSharedFolder(ctx, 42)
		Expect(err).NotTo(HaveOccurred())
	})

	It("sets and removes a member, escaping the principal", func() {
		_, err := New(serve(http.MethodPut, "/api/chats/admin/shared-folders/3/members/marta@example.com", map[string]any{"mode": "write"}, folder).URL, "").
			SetSharedFolderMember(ctx, 3, "marta@example.com", "write")
		Expect(err).NotTo(HaveOccurred())
		_, err = New(serve(http.MethodDelete, "/api/chats/admin/shared-folders/3/members/Marta%20Rossi", nil, folder).URL, "").RemoveSharedFolderMember(ctx, 3, "Marta Rossi")
		Expect(err).NotTo(HaveOccurred())
	})
})
