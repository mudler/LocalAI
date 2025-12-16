package config

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ApplicationConfig RuntimeSettings Conversion", func() {
	Describe("ToRuntimeSettings", func() {
		It("should convert all fields correctly", func() {
			appConfig := &ApplicationConfig{
				WatchDog:                 true,
				WatchDogIdle:             true,
				WatchDogBusy:             true,
				WatchDogIdleTimeout:      20 * time.Minute,
				WatchDogBusyTimeout:      10 * time.Minute,
				SingleBackend:            false,
				MaxActiveBackends:        5,
				ParallelBackendRequests:  true,
				MemoryReclaimerEnabled:   true,
				MemoryReclaimerThreshold: 0.85,
				Threads:                  8,
				ContextSize:              4096,
				F16:                      true,
				Debug:                    true,
				CORS:                     true,
				CSRF:                     true,
				CORSAllowOrigins:         "https://example.com",
				P2PToken:                 "test-token",
				P2PNetworkID:             "test-network",
				Federated:                true,
				Galleries:                []Gallery{{Name: "test-gallery", URL: "https://example.com"}},
				BackendGalleries:         []Gallery{{Name: "backend-gallery", URL: "https://example.com/backend"}},
				AutoloadGalleries:        true,
				AutoloadBackendGalleries: true,
				ApiKeys:                  []string{"key1", "key2"},
				AgentJobRetentionDays:    30,
			}

			rs := appConfig.ToRuntimeSettings()

			Expect(rs.WatchdogEnabled).ToNot(BeNil())
			Expect(*rs.WatchdogEnabled).To(BeTrue())

			Expect(rs.WatchdogIdleEnabled).ToNot(BeNil())
			Expect(*rs.WatchdogIdleEnabled).To(BeTrue())

			Expect(rs.WatchdogBusyEnabled).ToNot(BeNil())
			Expect(*rs.WatchdogBusyEnabled).To(BeTrue())

			Expect(rs.WatchdogIdleTimeout).ToNot(BeNil())
			Expect(*rs.WatchdogIdleTimeout).To(Equal("20m0s"))

			Expect(rs.WatchdogBusyTimeout).ToNot(BeNil())
			Expect(*rs.WatchdogBusyTimeout).To(Equal("10m0s"))

			Expect(rs.SingleBackend).ToNot(BeNil())
			Expect(*rs.SingleBackend).To(BeFalse())

			Expect(rs.MaxActiveBackends).ToNot(BeNil())
			Expect(*rs.MaxActiveBackends).To(Equal(5))

			Expect(rs.ParallelBackendRequests).ToNot(BeNil())
			Expect(*rs.ParallelBackendRequests).To(BeTrue())

			Expect(rs.MemoryReclaimerEnabled).ToNot(BeNil())
			Expect(*rs.MemoryReclaimerEnabled).To(BeTrue())

			Expect(rs.MemoryReclaimerThreshold).ToNot(BeNil())
			Expect(*rs.MemoryReclaimerThreshold).To(Equal(0.85))

			Expect(rs.Threads).ToNot(BeNil())
			Expect(*rs.Threads).To(Equal(8))

			Expect(rs.ContextSize).ToNot(BeNil())
			Expect(*rs.ContextSize).To(Equal(4096))

			Expect(rs.F16).ToNot(BeNil())
			Expect(*rs.F16).To(BeTrue())

			Expect(rs.Debug).ToNot(BeNil())
			Expect(*rs.Debug).To(BeTrue())

			Expect(rs.CORS).ToNot(BeNil())
			Expect(*rs.CORS).To(BeTrue())

			Expect(rs.CSRF).ToNot(BeNil())
			Expect(*rs.CSRF).To(BeTrue())

			Expect(rs.CORSAllowOrigins).ToNot(BeNil())
			Expect(*rs.CORSAllowOrigins).To(Equal("https://example.com"))

			Expect(rs.P2PToken).ToNot(BeNil())
			Expect(*rs.P2PToken).To(Equal("test-token"))

			Expect(rs.P2PNetworkID).ToNot(BeNil())
			Expect(*rs.P2PNetworkID).To(Equal("test-network"))

			Expect(rs.Federated).ToNot(BeNil())
			Expect(*rs.Federated).To(BeTrue())

			Expect(rs.Galleries).ToNot(BeNil())
			Expect(*rs.Galleries).To(HaveLen(1))
			Expect((*rs.Galleries)[0].Name).To(Equal("test-gallery"))

			Expect(rs.BackendGalleries).ToNot(BeNil())
			Expect(*rs.BackendGalleries).To(HaveLen(1))
			Expect((*rs.BackendGalleries)[0].Name).To(Equal("backend-gallery"))

			Expect(rs.AutoloadGalleries).ToNot(BeNil())
			Expect(*rs.AutoloadGalleries).To(BeTrue())

			Expect(rs.AutoloadBackendGalleries).ToNot(BeNil())
			Expect(*rs.AutoloadBackendGalleries).To(BeTrue())

			Expect(rs.ApiKeys).ToNot(BeNil())
			Expect(*rs.ApiKeys).To(HaveLen(2))
			Expect(*rs.ApiKeys).To(ContainElements("key1", "key2"))

			Expect(rs.AgentJobRetentionDays).ToNot(BeNil())
			Expect(*rs.AgentJobRetentionDays).To(Equal(30))
		})

		It("should use default timeouts when not set", func() {
			appConfig := &ApplicationConfig{}

			rs := appConfig.ToRuntimeSettings()

			Expect(rs.WatchdogIdleTimeout).ToNot(BeNil())
			Expect(*rs.WatchdogIdleTimeout).To(Equal("15m"))

			Expect(rs.WatchdogBusyTimeout).ToNot(BeNil())
			Expect(*rs.WatchdogBusyTimeout).To(Equal("5m"))
		})
	})

	Describe("ApplyRuntimeSettings", func() {
		It("should return false when settings is nil", func() {
			appConfig := &ApplicationConfig{}
			changed := appConfig.ApplyRuntimeSettings(nil)
			Expect(changed).To(BeFalse())
		})

		It("should only apply non-nil fields", func() {
			appConfig := &ApplicationConfig{
				WatchDog:    false,
				Threads:     4,
				ContextSize: 2048,
			}

			watchdogEnabled := true
			rs := &RuntimeSettings{
				WatchdogEnabled: &watchdogEnabled,
				// Leave other fields nil
			}

			changed := appConfig.ApplyRuntimeSettings(rs)

			Expect(changed).To(BeTrue())
			Expect(appConfig.WatchDog).To(BeTrue())
			// Unchanged fields should remain
			Expect(appConfig.Threads).To(Equal(4))
			Expect(appConfig.ContextSize).To(Equal(2048))
		})

		It("should apply watchdog settings and return changed=true", func() {
			appConfig := &ApplicationConfig{}

			watchdogEnabled := true
			watchdogIdle := true
			watchdogBusy := true
			idleTimeout := "30m"
			busyTimeout := "15m"

			rs := &RuntimeSettings{
				WatchdogEnabled:     &watchdogEnabled,
				WatchdogIdleEnabled: &watchdogIdle,
				WatchdogBusyEnabled: &watchdogBusy,
				WatchdogIdleTimeout: &idleTimeout,
				WatchdogBusyTimeout: &busyTimeout,
			}

			changed := appConfig.ApplyRuntimeSettings(rs)

			Expect(changed).To(BeTrue())
			Expect(appConfig.WatchDog).To(BeTrue())
			Expect(appConfig.WatchDogIdle).To(BeTrue())
			Expect(appConfig.WatchDogBusy).To(BeTrue())
			Expect(appConfig.WatchDogIdleTimeout).To(Equal(30 * time.Minute))
			Expect(appConfig.WatchDogBusyTimeout).To(Equal(15 * time.Minute))
		})

		It("should enable watchdog when idle is enabled", func() {
			appConfig := &ApplicationConfig{WatchDog: false}

			watchdogIdle := true
			rs := &RuntimeSettings{
				WatchdogIdleEnabled: &watchdogIdle,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.WatchDog).To(BeTrue())
			Expect(appConfig.WatchDogIdle).To(BeTrue())
		})

		It("should enable watchdog when busy is enabled", func() {
			appConfig := &ApplicationConfig{WatchDog: false}

			watchdogBusy := true
			rs := &RuntimeSettings{
				WatchdogBusyEnabled: &watchdogBusy,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.WatchDog).To(BeTrue())
			Expect(appConfig.WatchDogBusy).To(BeTrue())
		})

		It("should handle MaxActiveBackends and update SingleBackend accordingly", func() {
			appConfig := &ApplicationConfig{}

			maxBackends := 1
			rs := &RuntimeSettings{
				MaxActiveBackends: &maxBackends,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.MaxActiveBackends).To(Equal(1))
			Expect(appConfig.SingleBackend).To(BeTrue())

			// Test with multiple backends
			maxBackends = 5
			rs = &RuntimeSettings{
				MaxActiveBackends: &maxBackends,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.MaxActiveBackends).To(Equal(5))
			Expect(appConfig.SingleBackend).To(BeFalse())
		})

		It("should handle SingleBackend and update MaxActiveBackends accordingly", func() {
			appConfig := &ApplicationConfig{}

			singleBackend := true
			rs := &RuntimeSettings{
				SingleBackend: &singleBackend,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.SingleBackend).To(BeTrue())
			Expect(appConfig.MaxActiveBackends).To(Equal(1))

			// Test disabling single backend
			singleBackend = false
			rs = &RuntimeSettings{
				SingleBackend: &singleBackend,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.SingleBackend).To(BeFalse())
			Expect(appConfig.MaxActiveBackends).To(Equal(0))
		})

		It("should enable watchdog when memory reclaimer is enabled", func() {
			appConfig := &ApplicationConfig{WatchDog: false}

			memoryEnabled := true
			threshold := 0.90
			rs := &RuntimeSettings{
				MemoryReclaimerEnabled:   &memoryEnabled,
				MemoryReclaimerThreshold: &threshold,
			}

			changed := appConfig.ApplyRuntimeSettings(rs)

			Expect(changed).To(BeTrue())
			Expect(appConfig.WatchDog).To(BeTrue())
			Expect(appConfig.MemoryReclaimerEnabled).To(BeTrue())
			Expect(appConfig.MemoryReclaimerThreshold).To(Equal(0.90))
		})

		It("should reject invalid memory threshold values", func() {
			appConfig := &ApplicationConfig{MemoryReclaimerThreshold: 0.50}

			// Test threshold > 1.0
			invalidThreshold := 1.5
			rs := &RuntimeSettings{
				MemoryReclaimerThreshold: &invalidThreshold,
			}
			appConfig.ApplyRuntimeSettings(rs)
			Expect(appConfig.MemoryReclaimerThreshold).To(Equal(0.50)) // Should remain unchanged

			// Test threshold <= 0
			invalidThreshold = 0.0
			rs = &RuntimeSettings{
				MemoryReclaimerThreshold: &invalidThreshold,
			}
			appConfig.ApplyRuntimeSettings(rs)
			Expect(appConfig.MemoryReclaimerThreshold).To(Equal(0.50)) // Should remain unchanged

			// Test negative threshold
			invalidThreshold = -0.5
			rs = &RuntimeSettings{
				MemoryReclaimerThreshold: &invalidThreshold,
			}
			appConfig.ApplyRuntimeSettings(rs)
			Expect(appConfig.MemoryReclaimerThreshold).To(Equal(0.50)) // Should remain unchanged
		})

		It("should accept valid memory threshold at boundary", func() {
			appConfig := &ApplicationConfig{}

			// Test threshold = 1.0 (maximum valid)
			threshold := 1.0
			rs := &RuntimeSettings{
				MemoryReclaimerThreshold: &threshold,
			}
			appConfig.ApplyRuntimeSettings(rs)
			Expect(appConfig.MemoryReclaimerThreshold).To(Equal(1.0))

			// Test threshold just above 0
			threshold = 0.01
			rs = &RuntimeSettings{
				MemoryReclaimerThreshold: &threshold,
			}
			appConfig.ApplyRuntimeSettings(rs)
			Expect(appConfig.MemoryReclaimerThreshold).To(Equal(0.01))
		})

		It("should apply performance settings without triggering watchdog change", func() {
			appConfig := &ApplicationConfig{}

			threads := 16
			contextSize := 8192
			f16 := true
			debug := true

			rs := &RuntimeSettings{
				Threads:     &threads,
				ContextSize: &contextSize,
				F16:         &f16,
				Debug:       &debug,
			}

			changed := appConfig.ApplyRuntimeSettings(rs)

			// These settings don't require watchdog restart
			Expect(changed).To(BeFalse())
			Expect(appConfig.Threads).To(Equal(16))
			Expect(appConfig.ContextSize).To(Equal(8192))
			Expect(appConfig.F16).To(BeTrue())
			Expect(appConfig.Debug).To(BeTrue())
		})

		It("should apply CORS and security settings", func() {
			appConfig := &ApplicationConfig{}

			cors := true
			csrf := true
			origins := "https://example.com,https://other.com"

			rs := &RuntimeSettings{
				CORS:             &cors,
				CSRF:             &csrf,
				CORSAllowOrigins: &origins,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.CORS).To(BeTrue())
			Expect(appConfig.CSRF).To(BeTrue())
			Expect(appConfig.CORSAllowOrigins).To(Equal("https://example.com,https://other.com"))
		})

		It("should apply P2P settings", func() {
			appConfig := &ApplicationConfig{}

			token := "p2p-test-token"
			networkID := "p2p-test-network"
			federated := true

			rs := &RuntimeSettings{
				P2PToken:     &token,
				P2PNetworkID: &networkID,
				Federated:    &federated,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.P2PToken).To(Equal("p2p-test-token"))
			Expect(appConfig.P2PNetworkID).To(Equal("p2p-test-network"))
			Expect(appConfig.Federated).To(BeTrue())
		})

		It("should apply gallery settings", func() {
			appConfig := &ApplicationConfig{}

			galleries := []Gallery{
				{Name: "gallery1", URL: "https://gallery1.com"},
				{Name: "gallery2", URL: "https://gallery2.com"},
			}
			backendGalleries := []Gallery{
				{Name: "backend-gallery", URL: "https://backend.com"},
			}
			autoload := true
			autoloadBackend := true

			rs := &RuntimeSettings{
				Galleries:                &galleries,
				BackendGalleries:         &backendGalleries,
				AutoloadGalleries:        &autoload,
				AutoloadBackendGalleries: &autoloadBackend,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.Galleries).To(HaveLen(2))
			Expect(appConfig.Galleries[0].Name).To(Equal("gallery1"))
			Expect(appConfig.BackendGalleries).To(HaveLen(1))
			Expect(appConfig.AutoloadGalleries).To(BeTrue())
			Expect(appConfig.AutoloadBackendGalleries).To(BeTrue())
		})

		It("should apply agent settings", func() {
			appConfig := &ApplicationConfig{}

			retentionDays := 14

			rs := &RuntimeSettings{
				AgentJobRetentionDays: &retentionDays,
			}

			appConfig.ApplyRuntimeSettings(rs)

			Expect(appConfig.AgentJobRetentionDays).To(Equal(14))
		})
	})

	Describe("Round-trip conversion", func() {
		It("should maintain values through ToRuntimeSettings -> ApplyRuntimeSettings", func() {
			original := &ApplicationConfig{
				WatchDog:                 true,
				WatchDogIdle:             true,
				WatchDogBusy:             false,
				WatchDogIdleTimeout:      25 * time.Minute,
				WatchDogBusyTimeout:      12 * time.Minute,
				SingleBackend:            false,
				MaxActiveBackends:        3,
				ParallelBackendRequests:  true,
				MemoryReclaimerEnabled:   true,
				MemoryReclaimerThreshold: 0.92,
				Threads:                  12,
				ContextSize:              6144,
				F16:                      true,
				Debug:                    false,
				CORS:                     true,
				CSRF:                     false,
				CORSAllowOrigins:         "https://test.com",
				P2PToken:                 "round-trip-token",
				P2PNetworkID:             "round-trip-network",
				Federated:                true,
				AutoloadGalleries:        true,
				AutoloadBackendGalleries: false,
				AgentJobRetentionDays:    60,
			}

			// Convert to RuntimeSettings
			rs := original.ToRuntimeSettings()

			// Apply to a new ApplicationConfig
			target := &ApplicationConfig{}
			target.ApplyRuntimeSettings(&rs)

			// Verify all values match
			Expect(target.WatchDog).To(Equal(original.WatchDog))
			Expect(target.WatchDogIdle).To(Equal(original.WatchDogIdle))
			Expect(target.WatchDogBusy).To(Equal(original.WatchDogBusy))
			Expect(target.WatchDogIdleTimeout).To(Equal(original.WatchDogIdleTimeout))
			Expect(target.WatchDogBusyTimeout).To(Equal(original.WatchDogBusyTimeout))
			Expect(target.MaxActiveBackends).To(Equal(original.MaxActiveBackends))
			Expect(target.ParallelBackendRequests).To(Equal(original.ParallelBackendRequests))
			Expect(target.MemoryReclaimerEnabled).To(Equal(original.MemoryReclaimerEnabled))
			Expect(target.MemoryReclaimerThreshold).To(Equal(original.MemoryReclaimerThreshold))
			Expect(target.Threads).To(Equal(original.Threads))
			Expect(target.ContextSize).To(Equal(original.ContextSize))
			Expect(target.F16).To(Equal(original.F16))
			Expect(target.Debug).To(Equal(original.Debug))
			Expect(target.CORS).To(Equal(original.CORS))
			Expect(target.CSRF).To(Equal(original.CSRF))
			Expect(target.CORSAllowOrigins).To(Equal(original.CORSAllowOrigins))
			Expect(target.P2PToken).To(Equal(original.P2PToken))
			Expect(target.P2PNetworkID).To(Equal(original.P2PNetworkID))
			Expect(target.Federated).To(Equal(original.Federated))
			Expect(target.AutoloadGalleries).To(Equal(original.AutoloadGalleries))
			Expect(target.AutoloadBackendGalleries).To(Equal(original.AutoloadBackendGalleries))
			Expect(target.AgentJobRetentionDays).To(Equal(original.AgentJobRetentionDays))
		})

		It("should handle empty galleries correctly in round-trip", func() {
			original := &ApplicationConfig{
				Galleries:        []Gallery{},
				BackendGalleries: []Gallery{},
				ApiKeys:          []string{},
			}

			rs := original.ToRuntimeSettings()
			target := &ApplicationConfig{}
			target.ApplyRuntimeSettings(&rs)

			Expect(target.Galleries).To(BeEmpty())
			Expect(target.BackendGalleries).To(BeEmpty())
		})
	})

	Describe("Edge cases", func() {
		It("should handle invalid timeout string in ApplyRuntimeSettings", func() {
			appConfig := &ApplicationConfig{
				WatchDogIdleTimeout: 10 * time.Minute,
			}

			invalidTimeout := "not-a-duration"
			rs := &RuntimeSettings{
				WatchdogIdleTimeout: &invalidTimeout,
			}

			appConfig.ApplyRuntimeSettings(rs)

			// Should remain unchanged due to parse error
			Expect(appConfig.WatchDogIdleTimeout).To(Equal(10 * time.Minute))
		})

		It("should handle zero values in ApplicationConfig", func() {
			appConfig := &ApplicationConfig{
				// All zero values
			}

			rs := appConfig.ToRuntimeSettings()

			// Should still have non-nil pointers with zero/default values
			Expect(rs.WatchdogEnabled).ToNot(BeNil())
			Expect(*rs.WatchdogEnabled).To(BeFalse())

			Expect(rs.Threads).ToNot(BeNil())
			Expect(*rs.Threads).To(Equal(0))

			Expect(rs.MemoryReclaimerThreshold).ToNot(BeNil())
			Expect(*rs.MemoryReclaimerThreshold).To(Equal(0.0))
		})

		It("should prefer MaxActiveBackends over SingleBackend when both are set", func() {
			appConfig := &ApplicationConfig{}

			maxBackends := 3
			singleBackend := true

			rs := &RuntimeSettings{
				MaxActiveBackends: &maxBackends,
				SingleBackend:     &singleBackend,
			}

			appConfig.ApplyRuntimeSettings(rs)

			// MaxActiveBackends should take precedence
			Expect(appConfig.MaxActiveBackends).To(Equal(3))
			Expect(appConfig.SingleBackend).To(BeFalse()) // 3 != 1, so single backend is false
		})
	})
})
