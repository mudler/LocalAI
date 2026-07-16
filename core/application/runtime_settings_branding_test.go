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

	// Watchdog check interval (issue #10601). Unlike the idle/busy timeouts
	// (which default to 0), NewApplicationConfig baseline-defaults the
	// interval to 500ms. The loader's "apply file value only if still at the
	// zero default" env-detection therefore never fired for the interval, so
	// a UI-saved Check Interval silently reverted to 500ms on every restart
	// while the idle/busy timeouts persisted. These specs construct the
	// config the same way boot does (NewApplicationConfig) so they observe
	// the real default the loader sees.
	Describe("watchdog interval", func() {
		It("loads a UI-saved watchdog_interval on the next startup", func() {
			cfg := config.NewApplicationConfig()
			cfg.DynamicConfigsDir = seedSettings(`{"watchdog_interval": "2s"}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.WatchDogInterval).To(Equal(2 * time.Second))
		})

		It("does not override an explicit env/CLI interval", func() {
			cfg := config.NewApplicationConfig()
			cfg.DynamicConfigsDir = seedSettings(`{"watchdog_interval": "2s"}`)
			cfg.WatchDogInterval = 1 * time.Second // simulate SetWatchDogInterval from env
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.WatchDogInterval).To(Equal(1*time.Second), "env/CLI interval must win over the persisted file value")
		})
	})

	// MITM listener address. The file is the only source — no env var
	// exists — so a regression here means an admin who configured the
	// listener via /api/settings loses it after a reboot, even though
	// the value is still on disk in the volume. (Intercept hosts now
	// live in model YAML mitm.hosts: blocks, not runtime_settings.json.)
	Describe("MITM fields", func() {
		It("loads mitm_listen", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"mitm_listen": ":8443"}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.MITMListen).To(Equal(":8443"))
		})

		It("does not override an explicit CLI flag", func() {
			cfg := &config.ApplicationConfig{
				DynamicConfigsDir: seedSettings(`{"mitm_listen": ":8443"}`),
				MITMListen:        ":9999", // simulate WithMITMListen(":9999")
			}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.MITMListen).To(Equal(":9999"), "CLI flag must win over the persisted file value")
		})
	})

	// Instance-wide default PII detectors. The file is the only source (no
	// env var), and the loader runs immediately before startMITMIfConfigured,
	// so a regression here means the cloud-proxy MITM listener resolves an
	// empty detector set at boot and forwards intercepted traffic unredacted —
	// even though pii_default_detectors is on disk and the MITM model has PII
	// enabled. It also breaks request-side default redaction the same way.
	Describe("PII default detectors", func() {
		It("loads pii_default_detectors from the file", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"pii_default_detectors": ["privacy-filter-nemotron", "secret-filter"]}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.PIIDefaultDetectors).To(Equal([]string{"privacy-filter-nemotron", "secret-filter"}))
		})

		It("does not override an env/CLI-set value (LOCALAI_PII_DEFAULT_DETECTORS)", func() {
			cfg := &config.ApplicationConfig{
				DynamicConfigsDir:   seedSettings(`{"pii_default_detectors": ["from-file"]}`),
				PIIDefaultDetectors: []string{"from-env"}, // simulate WithPIIDefaultDetectors(env)
			}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.PIIDefaultDetectors).To(Equal([]string{"from-env"}), "env var must win over the persisted file value")
		})
	})

	// The live file watcher applies pii_default_detectors on a runtime change
	// the same way it handles galleries/threads/etc.: env-set values (current
	// == startup snapshot) are left alone, otherwise the file value is applied
	// to the live config so request-side default redaction picks it up without
	// a restart.
	Describe("file watcher: pii_default_detectors", func() {
		It("applies a changed file value to the live config", func() {
			startup := config.ApplicationConfig{} // no env baseline
			live := &config.ApplicationConfig{PIIDefaultDetectors: []string{"old"}}
			handler := readRuntimeSettingsJson(startup)
			Expect(handler([]byte(`{"pii_default_detectors":["new-a","new-b"]}`), live)).To(Succeed())
			Expect(live.PIIDefaultDetectors).To(Equal([]string{"new-a", "new-b"}))
		})

		It("leaves an env-controlled value untouched", func() {
			startup := config.ApplicationConfig{PIIDefaultDetectors: []string{"from-env"}}
			live := &config.ApplicationConfig{PIIDefaultDetectors: []string{"from-env"}}
			handler := readRuntimeSettingsJson(startup)
			Expect(handler([]byte(`{"pii_default_detectors":["from-file"]}`), live)).To(Succeed())
			Expect(live.PIIDefaultDetectors).To(Equal([]string{"from-env"}), "env-controlled detectors must not be overwritten by the file")
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

	// Issue #10845 root cause class: fields with non-zero boot defaults
	// whose == 0 guards never fired, so the persisted value was ignored on
	// every restart. The registry's baseline comparison fixes them; these
	// specs pin that.
	Describe("fields with non-zero defaults (silent restart loss)", func() {
		It("loads a persisted lru_eviction_max_retries over the default 30", func() {
			cfg := config.NewApplicationConfig()
			cfg.DynamicConfigsDir = seedSettings(`{"lru_eviction_max_retries": 99}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.LRUEvictionMaxRetries).To(Equal(99))
		})

		It("loads a persisted tracing_max_items over the default 1024", func() {
			cfg := config.NewApplicationConfig()
			cfg.DynamicConfigsDir = seedSettings(`{"tracing_max_items": 4096}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.TracingMaxItems).To(Equal(4096))
		})

		It("loads a persisted agent_job_retention_days over the default 30", func() {
			cfg := config.NewApplicationConfig()
			cfg.DynamicConfigsDir = seedSettings(`{"agent_job_retention_days": 7}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.AgentJobRetentionDays).To(Equal(7))
		})
	})

	// #10845: threads saved via the UI must survive a restart, while
	// LOCALAI_THREADS/CLI still wins. WithThreads no longer eagerly
	// resolves 0 to the physical-core count; application.New() does that
	// after this loader has run.
	Describe("performance fields", func() {
		It("loads persisted threads when env/CLI left them unset", func() {
			cfg := config.NewApplicationConfig(config.WithThreads(0))
			cfg.DynamicConfigsDir = seedSettings(`{"threads": 4}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.Threads).To(Equal(4))
		})

		It("keeps an env/CLI thread count over the file", func() {
			cfg := config.NewApplicationConfig(config.WithThreads(8))
			cfg.DynamicConfigsDir = seedSettings(`{"threads": 4}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.Threads).To(Equal(8))
		})

		It("loads persisted context_size and f16", func() {
			cfg := config.NewApplicationConfig()
			cfg.DynamicConfigsDir = seedSettings(`{"context_size": 4096, "f16": true}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.ContextSize).To(Equal(4096))
			Expect(cfg.F16).To(BeTrue())
		})
	})

	// Option-less boot contract with core/cli/run.go: the kong threshold
	// default (0.95) is injected unconditionally even when the reclaimer is
	// disabled, so DefaultRuntimeBaseline's 0.95 overlay matches reality and
	// a UI-persisted threshold is not mistaken for an env-set one. Both
	// persisted fields must apply, and enabling the reclaimer must force the
	// watchdog master flag (startup invariant).
	Describe("memory reclaimer", func() {
		It("applies a persisted enable + threshold on an option-less boot", func() {
			cfg := config.NewApplicationConfig()
			cfg.MemoryReclaimerThreshold = 0.95 // what run.go injects when no flag is passed
			cfg.DynamicConfigsDir = seedSettings(`{"memory_reclaimer_enabled": true, "memory_reclaimer_threshold": 0.5}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.MemoryReclaimerEnabled).To(BeTrue())
			Expect(cfg.MemoryReclaimerThreshold).To(Equal(0.5))
			Expect(cfg.WatchDog).To(BeTrue(), "an enabled reclaimer must force the watchdog on")
		})
	})

	// Backend logging capture. Worker/distributed mode force-enables it
	// (core/services/worker.SetBackendLoggingEnabled(true)); single mode used
	// to leave it off by default with no CLI flag, so the UI "Backend Logs"
	// page was silently empty unless the operator found the Settings toggle.
	// It now defaults on in single mode too. Because the default is on, the
	// loader must let a persisted enable_backend_logging=false (the UI
	// toggle-off) win over the default - the sticky "only flip false->true"
	// merge used for env-backed flags would otherwise ignore it and revert
	// the toggle on every restart.
	Describe("backend logging capture", func() {
		It("captures backend logs by default in single mode", func() {
			cfg := config.NewApplicationConfig()
			Expect(cfg.EnableBackendLogging).To(BeTrue(),
				"single mode should capture backend logs out of the box, matching worker mode")
		})

		It("honors a persisted enable_backend_logging=false across restart (toggle-off wins over default-on)", func() {
			cfg := config.NewApplicationConfig() // default-on boot state
			cfg.DynamicConfigsDir = seedSettings(`{"enable_backend_logging": false}`)
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.EnableBackendLogging).To(BeFalse(),
				"a UI toggle-off persisted to runtime_settings.json must survive a restart")
		})

		It("loads a persisted enable_backend_logging=true", func() {
			cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"enable_backend_logging": true}`)}
			loadRuntimeSettingsFromFile(cfg)
			Expect(cfg.EnableBackendLogging).To(BeTrue())
		})
	})
})
