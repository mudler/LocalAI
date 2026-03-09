package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAGI/core/state"
)

var _ = Describe("Agent CLI", func() {
	Describe("isJSONFile", func() {
		It("should detect .json suffix", func() {
			Expect(isJSONFile("agent.json")).To(BeTrue())
			Expect(isJSONFile("/path/to/config.json")).To(BeTrue())
		})

		It("should not detect non-existent path without .json suffix", func() {
			Expect(isJSONFile("my-agent-name")).To(BeFalse())
		})

		It("should detect existing file without .json suffix", func() {
			tmpFile, err := os.CreateTemp(GinkgoT().TempDir(), "agentconfig")
			Expect(err).ToNot(HaveOccurred())
			tmpFile.Close()
			Expect(isJSONFile(tmpFile.Name())).To(BeTrue())
		})

		It("should not detect a directory", func() {
			Expect(isJSONFile(GinkgoT().TempDir())).To(BeFalse())
		})
	})

	Describe("loadFromFile", func() {
		var dir string

		BeforeEach(func() {
			dir = GinkgoT().TempDir()
		})

		It("should load a valid config", func() {
			cfg := state.AgentConfig{
				Name:         "test-agent",
				Model:        "gpt-4",
				SystemPrompt: "You are a helpful assistant.",
				APIURL:       "http://localhost:8080",
			}
			data, err := json.Marshal(cfg)
			Expect(err).ToNot(HaveOccurred())

			path := filepath.Join(dir, "valid.json")
			Expect(os.WriteFile(path, data, 0644)).To(Succeed())

			cmd := &AgentRunCMD{}
			loaded, err := cmd.loadFromFile(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.Name).To(Equal("test-agent"))
			Expect(loaded.Model).To(Equal("gpt-4"))
			Expect(loaded.SystemPrompt).To(Equal("You are a helpful assistant."))
		})

		It("should return error for invalid JSON", func() {
			path := filepath.Join(dir, "invalid.json")
			Expect(os.WriteFile(path, []byte("{not valid json"), 0644)).To(Succeed())

			cmd := &AgentRunCMD{}
			_, err := cmd.loadFromFile(path)
			Expect(err).To(HaveOccurred())
		})

		It("should return error for nonexistent file", func() {
			cmd := &AgentRunCMD{}
			_, err := cmd.loadFromFile(filepath.Join(dir, "nonexistent.json"))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("loadFromRegistry", func() {
		It("should fetch successfully", func() {
			cfg := state.AgentConfig{
				Name:         "registry-agent",
				Model:        "llama3",
				SystemPrompt: "Hello from registry",
			}
			data, _ := json.Marshal(cfg)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/agents/registry-agent.json" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
			}))
			defer srv.Close()

			cmd := &AgentRunCMD{AgentHubURL: srv.URL}
			loaded, err := cmd.loadFromRegistry("registry-agent")
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.Name).To(Equal("registry-agent"))
			Expect(loaded.Model).To(Equal("llama3"))
		})

		It("should return error when agent not found", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			}))
			defer srv.Close()

			cmd := &AgentRunCMD{AgentHubURL: srv.URL}
			_, err := cmd.loadFromRegistry("nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("should return error on server error", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("internal error"))
			}))
			defer srv.Close()

			cmd := &AgentRunCMD{AgentHubURL: srv.URL}
			_, err := cmd.loadFromRegistry("broken")
			Expect(err).To(HaveOccurred())
		})

		It("should set name from ref when empty", func() {
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
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.Name).To(Equal("my-agent"))
		})

		It("should return error for invalid JSON response", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte("{invalid"))
			}))
			defer srv.Close()

			cmd := &AgentRunCMD{AgentHubURL: srv.URL}
			_, err := cmd.loadFromRegistry("bad-json")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("applyOverrides", func() {
		It("should fill empty fields", func() {
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

			Expect(cfg.APIURL).To(Equal("http://override:8080"))
			Expect(cfg.APIKey).To(Equal("key123"))
			Expect(cfg.Model).To(Equal("override-model"))
			Expect(cfg.MultimodalModel).To(Equal("mm-model"))
		})

		It("should not overwrite existing values", func() {
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

			Expect(cfg.APIURL).To(Equal("http://original:8080"))
			Expect(cfg.Model).To(Equal("original-model"))
		})
	})

	Describe("resolveAgentConfig", func() {
		It("should resolve from file", func() {
			dir := GinkgoT().TempDir()
			cfg := state.AgentConfig{Name: "file-agent", Model: "gpt-4"}
			data, _ := json.Marshal(cfg)
			path := filepath.Join(dir, "agent.json")
			Expect(os.WriteFile(path, data, 0644)).To(Succeed())

			cmd := &AgentRunCMD{AgentRef: path}
			loaded, err := cmd.resolveAgentConfig()
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.Name).To(Equal("file-agent"))
		})

		It("should resolve from registry", func() {
			cfg := state.AgentConfig{Name: "hub-agent", Model: "llama3"}
			data, _ := json.Marshal(cfg)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
			}))
			defer srv.Close()

			cmd := &AgentRunCMD{AgentRef: "hub-agent", AgentHubURL: srv.URL}
			loaded, err := cmd.resolveAgentConfig()
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.Name).To(Equal("hub-agent"))
		})
	})
})
