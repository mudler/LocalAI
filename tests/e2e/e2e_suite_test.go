package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	httpapi "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
	"github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"

	"github.com/mudler/xlog"
)

var (
	localAIURL       string
	anthropicBaseURL string
	tmpDir           string
	backendPath    string
	modelsPath     string
	configPath     string
	app            *echo.Echo
	appCtx         context.Context
	appCancel      context.CancelFunc
	client         *openai.Client
	apiPort        int
	apiURL         string
	mockBackendPath string
)

var _ = BeforeSuite(func() {
	var err error

	// Create temporary directory
	tmpDir, err = os.MkdirTemp("", "mock-backend-e2e-*")
	Expect(err).ToNot(HaveOccurred())

	backendPath = filepath.Join(tmpDir, "backends")
	modelsPath = filepath.Join(tmpDir, "models")
	Expect(os.MkdirAll(backendPath, 0755)).To(Succeed())
	Expect(os.MkdirAll(modelsPath, 0755)).To(Succeed())

	// Build mock backend
	mockBackendDir := filepath.Join("..", "e2e", "mock-backend")
	mockBackendPath = filepath.Join(backendPath, "mock-backend")

	// Check if mock-backend binary exists in the mock-backend directory
	possiblePaths := []string{
		filepath.Join(mockBackendDir, "mock-backend"),
		filepath.Join("tests", "e2e", "mock-backend", "mock-backend"),
		filepath.Join("..", "..", "tests", "e2e", "mock-backend", "mock-backend"),
	}

	found := false
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			mockBackendPath = p
			found = true
			break
		}
	}

	if !found {
		// Try to find it relative to current working directory
		wd, _ := os.Getwd()
		relPath := filepath.Join(wd, "..", "..", "tests", "e2e", "mock-backend", "mock-backend")
		if _, err := os.Stat(relPath); err == nil {
			mockBackendPath = relPath
			found = true
		}
	}

	Expect(found).To(BeTrue(), "mock-backend binary not found. Run 'make build-mock-backend' first")

	// Make sure it's executable
	Expect(os.Chmod(mockBackendPath, 0755)).To(Succeed())

	// Create model config YAML
	modelConfig := map[string]interface{}{
		"name":    "mock-model",
		"backend": "mock-backend",
		"parameters": map[string]interface{}{
			"model": "mock-model.bin",
		},
	}
	configPath = filepath.Join(modelsPath, "mock-model.yaml")
	configYAML, err := yaml.Marshal(modelConfig)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(configPath, configYAML, 0644)).To(Succeed())

	// Set up system state
	systemState, err := system.GetSystemState(
		system.WithBackendPath(backendPath),
		system.WithModelPath(modelsPath),
	)
	Expect(err).ToNot(HaveOccurred())

	// Create application
	appCtx, appCancel = context.WithCancel(context.Background())

	// Create application instance
	application, err := application.New(
		config.WithContext(appCtx),
		config.WithSystemState(systemState),
		config.WithDebug(true),
	)
	Expect(err).ToNot(HaveOccurred())

	// Register backend with application's model loader
	application.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)

	// Create HTTP app
	app, err = httpapi.API(application)
	Expect(err).ToNot(HaveOccurred())

	// Get free port
	port, err := freeport.GetFreePort()
	Expect(err).ToNot(HaveOccurred())
	apiPort = port
	apiURL = fmt.Sprintf("http://127.0.0.1:%d/v1", apiPort)
	localAIURL = apiURL
	// Anthropic SDK appends /v1/messages to base URL; use base without /v1 so requests go to /v1/messages
	anthropicBaseURL = fmt.Sprintf("http://127.0.0.1:%d", apiPort)

	// Start server in goroutine
	go func() {
		if err := app.Start(fmt.Sprintf("127.0.0.1:%d", apiPort)); err != nil && err != http.ErrServerClosed {
			xlog.Error("server error", "error", err)
		}
	}()

	// Wait for server to be ready
	defaultConfig := openai.DefaultConfig("")
	defaultConfig.BaseURL = apiURL
	client = openai.NewClientWithConfig(defaultConfig)

	Eventually(func() error {
		_, err := client.ListModels(context.TODO())
		return err
	}, "2m").ShouldNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	if appCancel != nil {
		appCancel()
	}
	if app != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(app.Shutdown(ctx)).To(Succeed())
	}
	if tmpDir != "" {
		os.RemoveAll(tmpDir)
	}
})

func TestLocalAI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI E2E test suite")
}
