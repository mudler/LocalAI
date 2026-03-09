package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAGI/core/state"
)

func TestIsJSONFile(t *testing.T) {
	// .json suffix should always be detected
	if !isJSONFile("agent.json") {
		t.Error("expected agent.json to be detected as JSON file")
	}
	if !isJSONFile("/path/to/config.json") {
		t.Error("expected /path/to/config.json to be detected as JSON file")
	}

	// Non-existent path without .json suffix is not a file
	if isJSONFile("my-agent-name") {
		t.Error("expected my-agent-name to not be detected as JSON file")
	}

	// Existing file without .json suffix should be detected
	tmpFile, err := os.CreateTemp(t.TempDir(), "agentconfig")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	if !isJSONFile(tmpFile.Name()) {
		t.Error("expected existing file to be detected as JSON file")
	}

	// Directory should not be detected
	if isJSONFile(t.TempDir()) {
		t.Error("expected directory to not be detected as JSON file")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid config", func(t *testing.T) {
		cfg := state.AgentConfig{
			Name:         "test-agent",
			Model:        "gpt-4",
			SystemPrompt: "You are a helpful assistant.",
			APIURL:       "http://localhost:8080",
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}

		path := filepath.Join(dir, "valid.json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}

		cmd := &AgentRunCMD{}
		loaded, err := cmd.loadFromFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded.Name != "test-agent" {
			t.Errorf("expected name 'test-agent', got %q", loaded.Name)
		}
		if loaded.Model != "gpt-4" {
			t.Errorf("expected model 'gpt-4', got %q", loaded.Model)
		}
		if loaded.SystemPrompt != "You are a helpful assistant." {
			t.Errorf("unexpected system prompt: %q", loaded.SystemPrompt)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		path := filepath.Join(dir, "invalid.json")
		if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
			t.Fatal(err)
		}

		cmd := &AgentRunCMD{}
		_, err := cmd.loadFromFile(path)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		cmd := &AgentRunCMD{}
		_, err := cmd.loadFromFile(filepath.Join(dir, "nonexistent.json"))
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})
}

func TestLoadFromRegistry(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		cfg := state.AgentConfig{
			Name:         "registry-agent",
			Model:        "llama3",
			SystemPrompt: "Hello from registry",
		}
		data, _ := json.Marshal(cfg)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/agents/registry-agent" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		}))
		defer srv.Close()

		cmd := &AgentRunCMD{AgentHubURL: srv.URL}
		loaded, err := cmd.loadFromRegistry("registry-agent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded.Name != "registry-agent" {
			t.Errorf("expected name 'registry-agent', got %q", loaded.Name)
		}
		if loaded.Model != "llama3" {
			t.Errorf("expected model 'llama3', got %q", loaded.Model)
		}
	})

	t.Run("agent not found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer srv.Close()

		cmd := &AgentRunCMD{AgentHubURL: srv.URL}
		_, err := cmd.loadFromRegistry("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent agent")
		}
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}))
		defer srv.Close()

		cmd := &AgentRunCMD{AgentHubURL: srv.URL}
		_, err := cmd.loadFromRegistry("broken")
		if err == nil {
			t.Error("expected error for server error")
		}
	})

	t.Run("sets name from ref when empty", func(t *testing.T) {
		cfg := state.AgentConfig{
			Model: "llama3",
		}
		data, _ := json.Marshal(cfg)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		}))
		defer srv.Close()

		cmd := &AgentRunCMD{AgentHubURL: srv.URL}
		loaded, err := cmd.loadFromRegistry("my-agent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded.Name != "my-agent" {
			t.Errorf("expected name 'my-agent' (from ref), got %q", loaded.Name)
		}
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("{invalid"))
		}))
		defer srv.Close()

		cmd := &AgentRunCMD{AgentHubURL: srv.URL}
		_, err := cmd.loadFromRegistry("bad-json")
		if err == nil {
			t.Error("expected error for invalid JSON response")
		}
	})
}

func TestApplyOverrides(t *testing.T) {
	t.Run("fills empty fields", func(t *testing.T) {
		cfg := &state.AgentConfig{Name: "test"}
		cmd := &AgentRunCMD{
			APIURL:             "http://override:8080",
			APIKey:             "key123",
			DefaultModel:       "override-model",
			MultimodalModel:    "mm-model",
			TranscriptionModel: "whisper",
			TTSModel:           "tts-1",
		}
		cmd.applyOverrides(cfg)

		if cfg.APIURL != "http://override:8080" {
			t.Errorf("expected APIURL override, got %q", cfg.APIURL)
		}
		if cfg.APIKey != "key123" {
			t.Errorf("expected APIKey override, got %q", cfg.APIKey)
		}
		if cfg.Model != "override-model" {
			t.Errorf("expected Model override, got %q", cfg.Model)
		}
		if cfg.MultimodalModel != "mm-model" {
			t.Errorf("expected MultimodalModel override, got %q", cfg.MultimodalModel)
		}
	})

	t.Run("does not overwrite existing values", func(t *testing.T) {
		cfg := &state.AgentConfig{
			Name:   "test",
			APIURL: "http://original:8080",
			Model:  "original-model",
		}
		cmd := &AgentRunCMD{
			APIURL:       "http://override:8080",
			DefaultModel: "override-model",
		}
		cmd.applyOverrides(cfg)

		if cfg.APIURL != "http://original:8080" {
			t.Errorf("expected original APIURL preserved, got %q", cfg.APIURL)
		}
		if cfg.Model != "original-model" {
			t.Errorf("expected original Model preserved, got %q", cfg.Model)
		}
	})
}

func TestResolveAgentConfig(t *testing.T) {
	t.Run("resolves from file", func(t *testing.T) {
		dir := t.TempDir()
		cfg := state.AgentConfig{Name: "file-agent", Model: "gpt-4"}
		data, _ := json.Marshal(cfg)
		path := filepath.Join(dir, "agent.json")
		os.WriteFile(path, data, 0644)

		cmd := &AgentRunCMD{AgentRef: path}
		loaded, err := cmd.resolveAgentConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded.Name != "file-agent" {
			t.Errorf("expected name 'file-agent', got %q", loaded.Name)
		}
	})

	t.Run("resolves from registry", func(t *testing.T) {
		cfg := state.AgentConfig{Name: "hub-agent", Model: "llama3"}
		data, _ := json.Marshal(cfg)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		}))
		defer srv.Close()

		cmd := &AgentRunCMD{AgentRef: "hub-agent", AgentHubURL: srv.URL}
		loaded, err := cmd.resolveAgentConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded.Name != "hub-agent" {
			t.Errorf("expected name 'hub-agent', got %q", loaded.Name)
		}
	})
}
