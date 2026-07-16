package config

import (
	"reflect"
	"strings"
	"time"

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
