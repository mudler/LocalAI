package config

import (
	"reflect"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/LocalAI/pkg/xsysinfo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("runtime settings registry", func() {
	// The registry is the single description of every runtime setting. If a
	// RuntimeSettings field has no registry row, the setting will be echoed,
	// persisted, or re-applied inconsistently - the exact bug class behind
	// #10845 (threads/context_size/f16 saved but ignored on restart). This
	// spec makes that a red test instead of a silent runtime regression.
	It("owns every RuntimeSettings field exactly once", func() {
		structFields := map[string]bool{}
		t := reflect.TypeOf(RuntimeSettings{})
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			Expect(name).NotTo(BeEmpty(), "RuntimeSettings.%s needs a json tag", t.Field(i).Name)
			structFields[name] = true
		}
		owned := map[string]int{}
		for _, f := range runtimeSettingsFields {
			for _, n := range f.jsonNames {
				owned[n]++
			}
		}
		for name := range structFields {
			Expect(owned[name]).To(Equal(1),
				"RuntimeSettings field %q must be owned by exactly one registry row - add it to runtimeSettingsFields in runtime_settings_registry.go", name)
		}
		for name := range owned {
			Expect(structFields[name]).To(BeTrue(),
				"registry row %q owns no RuntimeSettings field - remove or fix the row", name)
		}
	})

	// Catches a registry row wired to the wrong ApplicationConfig member:
	// a config with a distinct value in every settings-backed member must
	// survive To -> Apply -> To unchanged.
	It("round-trips every field through Apply and To", func() {
		// Applying vram_budget installs a process-global default cap via
		// xsysinfo.SetDefaultVRAMBudget; reset it so later specs don't
		// inherit a phantom 12GiB budget.
		DeferCleanup(func() {
			xsysinfo.SetDefaultVRAMBudget(vrambudget.Budget{})
		})
		src := NewApplicationConfig()
		src.WatchDog = true
		src.WatchDogIdle = true
		src.WatchDogBusy = true
		src.WatchDogIdleTimeout = 21 * time.Minute
		src.WatchDogBusyTimeout = 6 * time.Minute
		src.WatchDogInterval = 2 * time.Second
		src.MaxActiveBackends = 3
		src.SingleBackend = false
		src.AutoUpgradeBackends = true
		src.PreferDevelopmentBackends = true
		src.MemoryReclaimerEnabled = true
		src.MemoryReclaimerThreshold = 0.5
		src.ForceEvictionWhenBusy = true
		src.SizeAwareEviction = true
		src.LRUEvictionMaxRetries = 42
		src.LRUEvictionRetryInterval = 3 * time.Second
		src.Threads = 7
		src.ContextSize = 8192
		src.VRAMBudget = "12GiB"
		src.F16 = true
		src.Debug = true
		src.EnableTracing = true
		src.TracingMaxItems = 2048
		src.TracingMaxBodyBytes = 1234
		src.EnableBackendLogging = false
		src.CORS = true
		src.DisableCSRF = true
		src.CORSAllowOrigins = "https://example.com"
		src.P2PToken = "tok"
		src.P2PNetworkID = "netid"
		src.Federated = true
		src.Galleries = []Gallery{{Name: "g1", URL: "https://g1"}}
		src.BackendGalleries = []Gallery{{Name: "b1", URL: "https://b1"}}
		src.AutoloadGalleries = true
		src.AutoloadBackendGalleries = true
		src.AgentJobRetentionDays = 9
		src.OpenResponsesStoreTTL = time.Hour
		src.AgentPool.Enabled = false
		src.AgentPool.DefaultModel = "dm"
		src.AgentPool.EmbeddingModel = "em"
		src.AgentPool.MaxChunkingSize = 123
		src.AgentPool.ChunkOverlap = 12
		src.AgentPool.EnableLogs = true
		src.AgentPool.CollectionDBPath = "/tmp/c.db"
		src.AgentPool.VectorEngine = "postgres"
		src.AgentPool.DatabaseURL = "postgres://x"
		src.AgentPool.AgentHubURL = "https://hub"
		src.DisableLocalAIAssistant = true
		src.Distributed.DiskHeadroomDisabled = true
		src.Branding = BrandingConfig{
			InstanceName: "n", InstanceTagline: "t",
			LogoFile: "l.png", LogoHorizontalFile: "lh.png", FaviconFile: "f.ico",
		}
		src.MITMListen = ":8443"
		src.PIIDefaultDetectors = []string{"d1"}

		s1 := src.ToRuntimeSettings()
		dst := NewApplicationConfig()
		dst.ApplyRuntimeSettings(&s1)
		s2 := dst.ToRuntimeSettings()
		Expect(s2).To(Equal(s1))
	})
})

var _ = Describe("ApplyRuntimeSettingsAtStartup", func() {
	// DefaultRuntimeBaseline simulates the config an option-less
	// `local-ai run` boots with (NewApplicationConfig + the kong flag
	// defaults run.go always injects). A live value equal to the baseline
	// means env/CLI did not touch it, so the file may apply.
	It("applies persisted values whose non-zero defaults broke the old == 0 guards", func() {
		// Regression pins for the silent startup losses on master:
		// lru_eviction_max_retries (default 30), tracing_max_items (1024),
		// agent_job_retention_days (30), memory_reclaimer_threshold (0.95).
		o := DefaultRuntimeBaseline()
		retries, items, days, thr := 99, 4096, 7, 0.5
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{
			LRUEvictionMaxRetries:    &retries,
			TracingMaxItems:          &items,
			AgentJobRetentionDays:    &days,
			MemoryReclaimerThreshold: &thr,
		})
		Expect(o.LRUEvictionMaxRetries).To(Equal(99))
		Expect(o.TracingMaxItems).To(Equal(4096))
		Expect(o.AgentJobRetentionDays).To(Equal(7))
		Expect(o.MemoryReclaimerThreshold).To(Equal(0.5))
	})

	It("lets an env/CLI-set value win over the file", func() {
		o := DefaultRuntimeBaseline()
		o.Threads = 8 // simulate LOCALAI_THREADS=8
		fileThreads := 4
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{Threads: &fileThreads})
		Expect(o.Threads).To(Equal(8), "env value must win over the persisted file value")
	})

	It("applies persisted threads when env/CLI left them unset", func() {
		// Relies on WithThreads no longer eagerly resolving 0 (Task 5).
		o := DefaultRuntimeBaseline()
		fileThreads := 4
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{Threads: &fileThreads})
		Expect(o.Threads).To(Equal(4))
	})

	It("applies persisted galleries over the kong default list", func() {
		o := DefaultRuntimeBaseline()
		saved := []Gallery{{Name: "mine", URL: "https://example.com/index.yaml"}}
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{Galleries: &saved})
		Expect(o.Galleries).To(Equal(saved))
	})

	It("keeps env-configured galleries over the file", func() {
		o := DefaultRuntimeBaseline()
		o.Galleries = []Gallery{{Name: "env", URL: "https://env.example/index.yaml"}}
		saved := []Gallery{{Name: "file", URL: "https://file.example/index.yaml"}}
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{Galleries: &saved})
		Expect(o.Galleries[0].Name).To(Equal("env"))
	})

	It("always applies file-authoritative fields (backend logging toggle-off)", func() {
		o := DefaultRuntimeBaseline() // EnableBackendLogging defaults true
		off := false
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{EnableBackendLogging: &off})
		Expect(o.EnableBackendLogging).To(BeFalse())
	})

	It("raises the watchdog master flag when the file enables idle checks", func() {
		o := DefaultRuntimeBaseline()
		on := true
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{WatchdogIdleEnabled: &on})
		Expect(o.WatchDogIdle).To(BeTrue())
		Expect(o.WatchDog).To(BeTrue())
	})

	It("applies a persisted vram_budget when env/CLI left it unset", func() {
		// The vram_budget CLI flag has no kong default (empty string means
		// uncapped), so the baseline needs no overlay for it. Assert only
		// the config member: the xsysinfo default-budget side effect is
		// process-global and asserting it here would make sibling specs
		// order-dependent. Reset the cap afterwards for the same reason
		// (mirrors the persist suite's AfterEach).
		DeferCleanup(func() {
			xsysinfo.SetDefaultVRAMBudget(vrambudget.Budget{})
		})
		o := DefaultRuntimeBaseline()
		budget := "80%"
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{VRAMBudget: &budget})
		Expect(o.VRAMBudget).To(Equal("80%"))
	})

	It("is nil-safe", func() {
		o := DefaultRuntimeBaseline()
		o.ApplyRuntimeSettingsAtStartup(nil) // must not panic
	})
})
