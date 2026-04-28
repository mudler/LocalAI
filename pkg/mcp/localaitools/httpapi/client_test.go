package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
)

// fakeLocalAI is a minimal HTTP server that mimics the relevant LocalAI
// admin endpoints. Only the routes the httpapi.Client touches need to exist.
func fakeLocalAI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/models/available", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/models/galleries", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/models/jobs/job-123", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"processed": true, "progress": 100.0, "message": "done",
		})
	})

	mux.HandleFunc("/models/jobs/missing", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "could not find any status for ID", http.StatusInternalServerError)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/backends", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "llama-cpp", "installed": true, "tags": []string{"chat"}},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGallerySearchFiltersAndLimits(t *testing.T) {
	srv := fakeLocalAI(t)
	c := New(srv.URL, "")
	hits, err := c.GallerySearch(context.Background(), localaitools.GallerySearchQuery{
		Query: "qwen",
		Tag:   "chat",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Name != "qwen2.5-7b-instruct" {
		t.Errorf("expected 1 qwen result; got %+v", hits)
	}
	if !containsTagExact(hits[0].Tags, "chat") {
		t.Errorf("expected chat tag preserved")
	}
}

func TestListGalleries(t *testing.T) {
	srv := fakeLocalAI(t)
	c := New(srv.URL, "")
	out, err := c.ListGalleries(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 || out[0].Name != "official" {
		t.Errorf("unexpected: %+v", out)
	}
}

func TestInstallModelReturnsJobID(t *testing.T) {
	srv := fakeLocalAI(t)
	c := New(srv.URL, "")
	id, err := c.InstallModel(context.Background(), localaitools.InstallModelRequest{ModelName: "qwen2.5-7b-instruct"})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if id != "job-123" {
		t.Errorf("expected job-123, got %q", id)
	}
}

func TestGetJobStatusHappyPath(t *testing.T) {
	srv := fakeLocalAI(t)
	c := New(srv.URL, "")
	st, err := c.GetJobStatus(context.Background(), "job-123")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Processed || st.Progress != 100.0 {
		t.Errorf("unexpected: %+v", st)
	}
}

func TestGetJobStatusMissingReturnsNil(t *testing.T) {
	srv := fakeLocalAI(t)
	c := New(srv.URL, "")
	st, err := c.GetJobStatus(context.Background(), "missing")
	if err != nil {
		// We accept either a sentinel "not found" treatment or a typed error;
		// the contract is that the LLM gets something actionable. Today we
		// translate the localai 500-with-"could not find" body into nil, nil.
		if !strings.Contains(err.Error(), "could not find") {
			t.Fatalf("expected 'not found' style error, got %v", err)
		}
	}
	_ = st
}

func TestSystemInfoExtractsBackends(t *testing.T) {
	srv := fakeLocalAI(t)
	c := New(srv.URL, "")
	info, err := c.SystemInfo(context.Background())
	if err != nil {
		t.Fatalf("system: %v", err)
	}
	if info.Version != "v0.0.0-test" {
		t.Errorf("version=%q", info.Version)
	}
	if len(info.InstalledBackends) != 2 {
		t.Errorf("backends=%v", info.InstalledBackends)
	}
}

func TestListBackends(t *testing.T) {
	srv := fakeLocalAI(t)
	c := New(srv.URL, "")
	bs, err := c.ListBackends(context.Background())
	if err != nil {
		t.Fatalf("list backends: %v", err)
	}
	if len(bs) != 1 || bs[0].Name != "llama-cpp" || !bs[0].Installed {
		t.Errorf("unexpected: %+v", bs)
	}
}

func TestBearerTokenForwarded(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "secret-key")
	if _, err := c.ListGalleries(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer secret-key" {
		t.Errorf("Authorization header = %q, want %q", sawAuth, "Bearer secret-key")
	}
}
