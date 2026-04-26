package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAGI/core/state"
)

var _ = Describe("AgentRunCMD", func() {
	Describe("loadAgentConfig", func() {
		It("loads agent config from file", func() {
			tmpDir := GinkgoT().TempDir()
			configFile := filepath.Join(tmpDir, "agent.json")

			cfg := state.AgentConfig{
				Name:         "test-agent",
				Model:        "llama3",
				SystemPrompt: "You are a helpful assistant",
			}
			data, err := json.MarshalIndent(cfg, "", "  ")
			Expect(err).ToNot(HaveOccurred())
			Expect(os.WriteFile(configFile, data, 0644)).To(Succeed())

			cmd := &AgentRunCMD{
				Config:   configFile,
				StateDir: tmpDir,
			}

			loaded, err := cmd.loadAgentConfig()
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.Name).To(Equal("test-agent"))
			Expect(loaded.Model).To(Equal("llama3"))
		})

		It("loads agent config from pool", func() {
			tmpDir := GinkgoT().TempDir()

			pool := map[string]state.AgentConfig{
				"my-agent": {
					Model:        "gpt-4",
					Description:  "A test agent",
					SystemPrompt: "Hello",
				},
				"other-agent": {
					Model: "llama3",
				},
			}
			data, err := json.MarshalIndent(pool, "", "  ")
			Expect(err).ToNot(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(tmpDir, "pool.json"), data, 0644)).To(Succeed())

			cmd := &AgentRunCMD{
				Name:     "my-agent",
				StateDir: tmpDir,
			}

			loaded, err := cmd.loadAgentConfig()
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.Name).To(Equal("my-agent"))
			Expect(loaded.Model).To(Equal("gpt-4"))
		})

		It("returns error for missing agent in pool", func() {
			tmpDir := GinkgoT().TempDir()

			pool := map[string]state.AgentConfig{
				"existing-agent": {Model: "llama3"},
			}
			data, err := json.MarshalIndent(pool, "", "  ")
			Expect(err).ToNot(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(tmpDir, "pool.json"), data, 0644)).To(Succeed())

			cmd := &AgentRunCMD{
				Name:     "nonexistent",
				StateDir: tmpDir,
			}

			_, err = cmd.loadAgentConfig()
			Expect(err).To(HaveOccurred())
		})

		It("returns error when no pool.json exists", func() {
			cmd := &AgentRunCMD{
				StateDir: GinkgoT().TempDir(),
			}

			_, err := cmd.loadAgentConfig()
			Expect(err).To(HaveOccurred())
		})

		It("returns error for config with no name", func() {
			tmpDir := GinkgoT().TempDir()
			configFile := filepath.Join(tmpDir, "agent.json")

			cfg := state.AgentConfig{
				Model: "llama3",
			}
			data, _ := json.MarshalIndent(cfg, "", "  ")
			Expect(os.WriteFile(configFile, data, 0644)).To(Succeed())

			cmd := &AgentRunCMD{
				Config:   configFile,
				StateDir: tmpDir,
			}

			_, err := cmd.loadAgentConfig()
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("applyOverrides", func() {
		It("applies overrides to empty fields", func() {
			cfg := &state.AgentConfig{
				Name: "test",
			}

			cmd := &AgentRunCMD{
				APIURL:       "http://localhost:9090",
				APIKey:       "secret",
				DefaultModel: "my-model",
			}

			cmd.applyOverrides(cfg)

			Expect(cfg.APIURL).To(Equal("http://localhost:9090"))
			Expect(cfg.APIKey).To(Equal("secret"))
			Expect(cfg.Model).To(Equal("my-model"))
		})

		It("does not overwrite existing model", func() {
			cfg := &state.AgentConfig{
				Name:  "test",
				Model: "existing-model",
			}

			cmd := &AgentRunCMD{
				DefaultModel: "override-model",
			}

			cmd.applyOverrides(cfg)

			Expect(cfg.Model).To(Equal("existing-model"))
		})
	})
})

var _ = Describe("AgentListCMD", func() {
	It("runs without error when no pool file exists", func() {
		cmd := &AgentListCMD{
			StateDir: GinkgoT().TempDir(),
		}
		Expect(cmd.Run(nil)).To(Succeed())
	})

	It("runs without error with agents in pool", func() {
		tmpDir := GinkgoT().TempDir()

		pool := map[string]state.AgentConfig{
			"agent-a": {Model: "llama3", Description: "First agent"},
			"agent-b": {Model: "gpt-4"},
		}
		data, _ := json.MarshalIndent(pool, "", "  ")
		Expect(os.WriteFile(filepath.Join(tmpDir, "pool.json"), data, 0644)).To(Succeed())

		cmd := &AgentListCMD{
			StateDir: tmpDir,
		}
		Expect(cmd.Run(nil)).To(Succeed())
	})
})
