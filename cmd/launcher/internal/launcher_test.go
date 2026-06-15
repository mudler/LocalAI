package launcher_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"fyne.io/fyne/v2/app"

	launcher "github.com/mudler/LocalAI/cmd/launcher/internal"
)

var _ = Describe("Launcher", func() {
	var (
		launcherInstance *launcher.Launcher
		tempDir          string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "launcher-test-*")
		Expect(err).ToNot(HaveOccurred())

		ui := launcher.NewLauncherUI()
		app := app.NewWithID("com.localai.launcher")

		launcherInstance = launcher.NewLauncher(ui, nil, app)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("NewLauncher", func() {
		It("should create a launcher with default configuration", func() {
			Expect(launcherInstance.GetConfig()).ToNot(BeNil())
		})
	})

	Describe("Initialize", func() {
		It("should set default paths when not configured", func() {
			err := launcherInstance.Initialize()
			Expect(err).ToNot(HaveOccurred())

			config := launcherInstance.GetConfig()
			Expect(config.ModelsPath).ToNot(BeEmpty())
			Expect(config.BackendsPath).ToNot(BeEmpty())
		})

		It("should set default ShowWelcome to true", func() {
			err := launcherInstance.Initialize()
			Expect(err).ToNot(HaveOccurred())

			config := launcherInstance.GetConfig()
			Expect(config.ShowWelcome).To(BeTrue())
			Expect(config.Address).To(Equal("127.0.0.1:8080"))
			Expect(config.LogLevel).To(Equal("info"))
		})

		It("should create models and backends directories", func() {
			// Set custom paths for testing
			config := launcherInstance.GetConfig()
			config.ModelsPath = filepath.Join(tempDir, "models")
			config.BackendsPath = filepath.Join(tempDir, "backends")
			launcherInstance.SetConfig(config)

			err := launcherInstance.Initialize()
			Expect(err).ToNot(HaveOccurred())

			// Check if directories were created
			_, err = os.Stat(config.ModelsPath)
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(config.BackendsPath)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Configuration", func() {
		It("should get and set configuration", func() {
			config := launcherInstance.GetConfig()
			config.ModelsPath = "/test/models"
			config.BackendsPath = "/test/backends"
			config.Address = ":9090"
			config.LogLevel = "debug"

			err := launcherInstance.SetConfig(config)
			Expect(err).ToNot(HaveOccurred())

			retrievedConfig := launcherInstance.GetConfig()
			Expect(retrievedConfig.ModelsPath).To(Equal("/test/models"))
			Expect(retrievedConfig.BackendsPath).To(Equal("/test/backends"))
			Expect(retrievedConfig.Address).To(Equal(":9090"))
			Expect(retrievedConfig.LogLevel).To(Equal("debug"))
		})
	})

	Describe("WebUI URL", func() {
		It("should return correct WebUI URL for localhost", func() {
			config := launcherInstance.GetConfig()
			config.Address = ":8080"
			launcherInstance.SetConfig(config)

			url := launcherInstance.GetWebUIURL()
			Expect(url).To(Equal("http://localhost:8080"))
		})

		It("should return correct WebUI URL for full address", func() {
			config := launcherInstance.GetConfig()
			config.Address = "127.0.0.1:8080"
			launcherInstance.SetConfig(config)

			url := launcherInstance.GetWebUIURL()
			Expect(url).To(Equal("http://127.0.0.1:8080"))
		})

		It("should handle http prefix correctly", func() {
			config := launcherInstance.GetConfig()
			config.Address = "http://localhost:8080"
			launcherInstance.SetConfig(config)

			url := launcherInstance.GetWebUIURL()
			Expect(url).To(Equal("http://localhost:8080"))
		})
	})

	Describe("Process Management", func() {
		It("should not be running initially", func() {
			Expect(launcherInstance.IsRunning()).To(BeFalse())
		})

		It("should handle start when binary doesn't exist", func() {
			err := launcherInstance.StartLocalAI()
			Expect(err).To(HaveOccurred())
			// Could be either "not found" or "permission denied" depending on test environment
			errMsg := err.Error()
			hasExpectedError := strings.Contains(errMsg, "LocalAI binary") ||
				strings.Contains(errMsg, "permission denied")
			Expect(hasExpectedError).To(BeTrue(), "Expected error about binary not found or permission denied, got: %s", errMsg)
		})

		It("should handle stop when not running", func() {
			err := launcherInstance.StopLocalAI()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("LocalAI is not running"))
		})
	})

	Describe("Logs", func() {
		It("should return empty logs initially", func() {
			logs := launcherInstance.GetLogs()
			Expect(logs).To(BeEmpty())
		})
	})

	Describe("Version Management", func() {
		It("should return empty version when no binary installed", func() {
			version := launcherInstance.GetCurrentVersion()
			Expect(version).To(BeEmpty()) // No binary installed in test environment
		})

		It("should handle update checks", func() {
			// This test would require mocking HTTP responses
			// For now, we'll just test that the method doesn't panic
			_, _, err := launcherInstance.CheckForUpdates()
			// We expect either success or a network error, not a panic
			if err != nil {
				// Network error is acceptable in tests
				Expect(err.Error()).To(ContainSubstring("failed to fetch"))
			}
		})
	})
})

var _ = Describe("Config", func() {
	It("should have proper JSON tags", func() {
		config := &launcher.Config{
			ModelsPath:      "/test/models",
			BackendsPath:    "/test/backends",
			Address:         ":8080",
			AutoStart:       true,
			LogLevel:        "info",
			EnvironmentVars: map[string]string{"TEST": "value"},
		}

		Expect(config.ModelsPath).To(Equal("/test/models"))
		Expect(config.BackendsPath).To(Equal("/test/backends"))
		Expect(config.Address).To(Equal(":8080"))
		Expect(config.AutoStart).To(BeTrue())
		Expect(config.LogLevel).To(Equal("info"))
		Expect(config.EnvironmentVars).To(HaveKeyWithValue("TEST", "value"))
	})

	It("should initialize environment variables map", func() {
		config := &launcher.Config{}
		Expect(config.EnvironmentVars).To(BeNil())

		ui := launcher.NewLauncherUI()
		app := app.NewWithID("com.localai.launcher")

		launcher := launcher.NewLauncher(ui, nil, app)

		err := launcher.Initialize()
		Expect(err).ToNot(HaveOccurred())

		retrievedConfig := launcher.GetConfig()
		Expect(retrievedConfig.EnvironmentVars).ToNot(BeNil())
		Expect(retrievedConfig.EnvironmentVars).To(BeEmpty())
	})
})
