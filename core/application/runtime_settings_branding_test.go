package application

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// seedSettings writes the given JSON fragment to runtime_settings.json
// under a fresh temp DynamicConfigsDir and returns the directory path.
func seedSettings(json string) string {
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "runtime_settings.json"), []byte(json), 0o600)).To(Succeed())
	return dir
}

var _ = Describe("loadRuntimeSettingsFromFile", func() {
	// Reproduces the "settings revert after restart" report: an admin
	// sets a branding instance name + uploads a logo, the values are
	// persisted to runtime_settings.json, but on the next startup
	// loadRuntimeSettingsFromFile() did not read those fields back so
	// appConfig.Branding stayed zero and the public /api/branding
	// endpoint fell back to LocalAI defaults.
	Describe("branding fields", func() {
		It("loads instance name, tagline, and asset basenames", func() {
			dir := seedSettings(`{
                "instance_name": "Acme AI",
                "instance_tagline": "Private inference",
                "logo_file": "logo.png",
                "logo_horizontal_file": "logo_horizontal.svg",
                "favicon_file": "favicon.ico"
            }`)

			cfg := &config.ApplicationConfig{DynamicConfigsDir: dir}
			loadRuntimeSettingsFromFile(cfg)

			Expect(cfg.Branding).To(Equal(config.BrandingConfig{
				InstanceName:       "Acme AI",
				InstanceTagline:    "Private inference",
				LogoFile:           "logo.png",
				LogoHorizontalFile: "logo_horizontal.svg",
				FaviconFile:        "favicon.ico",
			}))
		})
	})

	// Adjacent fields exercise the other classes of settings that
	// previously silently reverted on restart. Each spec pairs a
	// runtime_settings.json fragment with the expected ApplicationConfig
	// state after the loader runs. A regression in any one means a
	// UI-saved setting will not survive a process restart — same shape as
	// the branding bug, different field.
	//
	// Where a field has a non-zero default (set by NewApplicationConfig),
	// the spec seeds the post-AppOptions state the loader would observe
	// at boot. Without that setup the "if at default" gate would either
	// always pass or always fail and the spec wouldn't reflect the real
	// call site.
	Describe("adjacent restart-loss fields", func() {
		It("loads auto_upgrade_backends", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"auto_upgrade_backends": true}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AutoUpgradeBackends).To(BeTrue())
		})

		It("loads prefer_development_backends", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"prefer_development_backends": true}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.PreferDevelopmentBackends).To(BeTrue())
		})

		It("disables the LocalAI Assistant when localai_assistant_enabled=false", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"localai_assistant_enabled": false}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.DisableLocalAIAssistant).To(BeTrue())
		})

		It("loads open_responses_store_ttl as a duration", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"open_responses_store_ttl": "1h"}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.OpenResponsesStoreTTL).To(Equal(time.Hour))
		})
	})

	// The Agent Pool block has a mix of zero and non-zero defaults
	// (Enabled=true, EmbeddingModel="granite-...", MaxChunkingSize=400,
	// VectorEngine="chromem", AgentHubURL="https://agenthub.localai.io").
	// Each spec seeds the appropriate startup state so the loader's
	// "at default" check observes what New() would.
	Describe("agent pool fields", func() {
		It("loads agent_pool_enabled=false against the default-true", func() {
			cfg := &config.ApplicationConfig{
				DynamicConfigsDir: seedSettings(`{"agent_pool_enabled": false}`),
				AgentPool:         config.AgentPoolConfig{Enabled: true},
			}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.Enabled).To(BeFalse())
		})

		It("loads agent_pool_default_model", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"agent_pool_default_model": "qwen2.5-7b"}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.DefaultModel).To(Equal("qwen2.5-7b"))
		})

		It("overrides the granite embedding default", func() {
			cfg := &config.ApplicationConfig{
				DynamicConfigsDir: seedSettings(`{"agent_pool_embedding_model": "all-minilm"}`),
				AgentPool:         config.AgentPoolConfig{EmbeddingModel: "granite-embedding-107m-multilingual"},
			}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.EmbeddingModel).To(Equal("all-minilm"))
		})

		It("overrides the 400 max chunking size default", func() {
			cfg := &config.ApplicationConfig{
				DynamicConfigsDir: seedSettings(`{"agent_pool_max_chunking_size": 800}`),
				AgentPool:         config.AgentPoolConfig{MaxChunkingSize: 400},
			}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.MaxChunkingSize).To(Equal(800))
		})

		It("loads agent_pool_chunk_overlap", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"agent_pool_chunk_overlap": 50}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.ChunkOverlap).To(Equal(50))
		})

		It("loads agent_pool_enable_logs", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"agent_pool_enable_logs": true}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.EnableLogs).To(BeTrue())
		})

		It("loads agent_pool_collection_db_path", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"agent_pool_collection_db_path": "/var/lib/localai/collections.db"}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.CollectionDBPath).To(Equal("/var/lib/localai/collections.db"))
		})

		It("overrides the chromem vector_engine default", func() {
			cfg := &config.ApplicationConfig{
				DynamicConfigsDir: seedSettings(`{"agent_pool_vector_engine": "postgres"}`),
				AgentPool:         config.AgentPoolConfig{VectorEngine: "chromem"},
			}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.VectorEngine).To(Equal("postgres"))
		})

		It("loads agent_pool_database_url", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"agent_pool_database_url": "postgres://user:pass@db:5432/localai"}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.DatabaseURL).To(Equal("postgres://user:pass@db:5432/localai"))
		})

		It("overrides the agenthub.localai.io agent_hub_url default", func() {
			cfg := &config.ApplicationConfig{
				DynamicConfigsDir: seedSettings(`{"agent_pool_agent_hub_url": "https://hub.acme.io"}`),
				AgentPool:         config.AgentPoolConfig{AgentHubURL: "https://agenthub.localai.io"},
			}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentPool.AgentHubURL).To(Equal("https://hub.acme.io"))
		})
	})
})
