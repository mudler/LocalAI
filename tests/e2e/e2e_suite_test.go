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
	localaiapp "github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	httpapi "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
	"gopkg.in/yaml.v3"

	"github.com/mudler/xlog"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

var (
	anthropicBaseURL  string
	ollamaBaseURL     string
	tmpDir            string
	backendPath       string
	modelsPath        string
	configPath        string
	app               *echo.Echo
	appCtx            context.Context
	appCancel         context.CancelFunc
	client            openai.Client
	apiPort           int
	apiURL            string
	mockBackendPath   string
	cloudProxyPath    string
	mcpServerURL      string
	mcpServerShutdown func()
	localAIApp        *localaiapp.Application

	// Cloud-proxy fake upstreams. Live for the whole suite so the four
	// cloud-proxy model YAMLs can point at their URLs at startup time.
	cpOpenAIUpstream    *fakeOpenAIUpstreamServer
	cpAnthropicUpstream *fakeAnthropicUpstreamServer
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
	modelConfig := map[string]any{
		"name":    "mock-model",
		"backend": "mock-backend",
		"parameters": map[string]any{
			"model": "mock-model.bin",
		},
	}
	configPath = filepath.Join(modelsPath, "mock-model.yaml")
	configYAML, err := yaml.Marshal(modelConfig)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(configPath, configYAML, 0644)).To(Succeed())

	// Create model config for autoparser tests (NoGrammar so tool calls
	// are driven entirely by the backend's ChatDeltas, not grammar enforcement)
	autoparserConfig := map[string]any{
		"name":    "mock-model-autoparser",
		"backend": "mock-backend",
		"parameters": map[string]any{
			"model": "mock-model.bin",
		},
		"function": map[string]any{
			"grammar": map[string]any{
				"disable": true,
			},
		},
	}
	autoparserPath := filepath.Join(modelsPath, "mock-model-autoparser.yaml")
	autoparserYAML, err := yaml.Marshal(autoparserConfig)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(autoparserPath, autoparserYAML, 0644)).To(Succeed())

	// Create model config for thinking model + autoparser tests.
	// The chat template ends with <|channel>thought to simulate Gemma 4 thinking models.
	// This triggers DetectThinkingStartToken and PrependThinkingTokenIfNeeded in the
	// reasoning extraction path, reproducing a bug where clean content from the C++
	// autoparser gets misclassified as unclosed reasoning.
	thinkingAutoparserConfig := map[string]any{
		"name":    "mock-model-thinking-autoparser",
		"backend": "mock-backend",
		"parameters": map[string]any{
			"model": "mock-model.bin",
		},
		"template": map[string]any{
			"chat": "{{.Input}}\n<|turn>model\n<|channel>thought\n<channel|>",
		},
		"function": map[string]any{
			"grammar": map[string]any{
				"disable": true,
			},
		},
	}
	thinkingAutoparserPath := filepath.Join(modelsPath, "mock-model-thinking-autoparser.yaml")
	thinkingAutoparserYAML, err := yaml.Marshal(thinkingAutoparserConfig)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(thinkingAutoparserPath, thinkingAutoparserYAML, 0644)).To(Succeed())

	// Start mock MCP server and create MCP-enabled model config
	mcpServerURL, mcpServerShutdown = startMockMCPServer()
	mcpConfig := mcpModelConfig(mcpServerURL)
	mcpConfigPath := filepath.Join(modelsPath, "mock-model-mcp.yaml")
	mcpConfigYAML, err := yaml.Marshal(mcpConfig)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(mcpConfigPath, mcpConfigYAML, 0644)).To(Succeed())

	// Create pipeline model configs for realtime API tests.
	// Each component model uses the same mock-backend binary.
	for _, name := range []string{"mock-vad", "mock-stt", "mock-llm", "mock-tts"} {
		cfg := map[string]any{
			"name":    name,
			"backend": "mock-backend",
			"parameters": map[string]any{
				"model": name + ".bin",
			},
		}
		data, err := yaml.Marshal(cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(modelsPath, name+".yaml"), data, 0644)).To(Succeed())
	}

	// Path-resolution model — declares relative draft_model / mmproj paths
	// so the e2e test can confirm they arrive at the backend resolved
	// against the models directory (regression guard for issue #9675).
	pathResolutionCfg := map[string]any{
		"name":    "mock-model-path-resolution",
		"backend": "mock-backend",
		"parameters": map[string]any{
			"model": "subdir/mock-main.bin",
		},
		"draft_model": "subdir/mock-draft.bin",
		"mmproj":      "subdir/mock-mmproj.bin",
	}
	pathResolutionData, err := yaml.Marshal(pathResolutionCfg)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelsPath, "mock-model-path-resolution.yaml"), pathResolutionData, 0644)).To(Succeed())

	// Same but with an absolute draft_model — must not let a user-supplied
	// config reach files outside the models directory. filepath.Join
	// strips the leading slash, so /etc/passwd becomes <modelsPath>/etc/passwd.
	pathEscapeCfg := map[string]any{
		"name":    "mock-model-path-escape",
		"backend": "mock-backend",
		"parameters": map[string]any{
			"model": "subdir/mock-main.bin",
		},
		"draft_model": "/etc/passwd",
	}
	pathEscapeData, err := yaml.Marshal(pathEscapeCfg)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelsPath, "mock-model-path-escape.yaml"), pathEscapeData, 0644)).To(Succeed())

	// Diarization model — known_usecases bypasses the FLAG_DIARIZATION
	// backend-name guard so the /v1/audio/diarization route can dispatch
	// to the mock backend.
	diarizeCfg := map[string]any{
		"name":           "mock-diarize",
		"backend":        "mock-backend",
		"known_usecases": []string{"FLAG_DIARIZATION"},
		"parameters": map[string]any{
			"model": "mock-diarize.bin",
		},
	}
	diarizeData, err := yaml.Marshal(diarizeCfg)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelsPath, "mock-diarize.yaml"), diarizeData, 0644)).To(Succeed())

	// Pipeline model that wires the component models together.
	pipelineCfg := map[string]any{
		"name": "realtime-pipeline",
		"pipeline": map[string]any{
			"vad":           "mock-vad",
			"transcription": "mock-stt",
			"llm":           "mock-llm",
			"tts":           "mock-tts",
		},
	}
	pipelineData, err := yaml.Marshal(pipelineCfg)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelsPath, "realtime-pipeline.yaml"), pipelineData, 0644)).To(Succeed())

	// If REALTIME_TEST_MODEL=realtime-test-pipeline, auto-create a pipeline
	// config from the REALTIME_VAD/STT/LLM/TTS env vars so real-model tests
	// can run without the user having to write a YAML file manually.
	if os.Getenv("REALTIME_TEST_MODEL") == "realtime-test-pipeline" {
		rtVAD := os.Getenv("REALTIME_VAD")
		rtSTT := os.Getenv("REALTIME_STT")
		rtLLM := os.Getenv("REALTIME_LLM")
		rtTTS := os.Getenv("REALTIME_TTS")

		if rtVAD != "" && rtSTT != "" && rtLLM != "" && rtTTS != "" {
			testPipeline := map[string]any{
				"name": "realtime-test-pipeline",
				"pipeline": map[string]any{
					"vad":           rtVAD,
					"transcription": rtSTT,
					"llm":           rtLLM,
					"tts":           rtTTS,
				},
			}
			data, writeErr := yaml.Marshal(testPipeline)
			Expect(writeErr).ToNot(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(modelsPath, "realtime-test-pipeline.yaml"), data, 0644)).To(Succeed())
			xlog.Info("created realtime-test-pipeline",
				"vad", rtVAD, "stt", rtSTT, "llm", rtLLM, "tts", rtTTS)
		}
	}

	// Import model configs from an external directory (e.g. real model YAMLs
	// and weights mounted into a container). Symlinks avoid copying large files.
	// Both files and directories are symlinked — multi-file backends like
	// sherpa-onnx TTS expect their tokens.txt / lexicon.txt sidecars in the
	// same directory as the .onnx, so we need whole-directory imports.
	if rtModels := os.Getenv("REALTIME_MODELS_PATH"); rtModels != "" {
		entries, err := os.ReadDir(rtModels)
		Expect(err).ToNot(HaveOccurred())
		for _, entry := range entries {
			src := filepath.Join(rtModels, entry.Name())
			dst := filepath.Join(modelsPath, entry.Name())
			if _, err := os.Stat(dst); err == nil {
				continue // don't overwrite mock configs
			}
			Expect(os.Symlink(src, dst)).To(Succeed())
		}
	}

	// Set up system state. When REALTIME_BACKENDS_PATH is set, use it so the
	// application can discover real backend binaries for real-model tests.
	systemOpts := []system.SystemStateOptions{
		system.WithModelPath(modelsPath),
	}
	if realBackends := os.Getenv("REALTIME_BACKENDS_PATH"); realBackends != "" {
		systemOpts = append(systemOpts, system.WithBackendPath(realBackends))
	} else {
		systemOpts = append(systemOpts, system.WithBackendPath(backendPath))
	}

	// Cloud-proxy backend e2e setup. The cloud-proxy binary lives next
	// to mock-backend and is registered under its canonical "cloud-proxy"
	// name. Fake upstreams come up first so the model YAMLs can encode
	// their URLs at startup time. Build is best-effort — when the binary
	// isn't present, the cloud-proxy specs Skip and the rest of the
	// suite is unaffected.
	cloudProxyCandidates := []string{
		filepath.Join("..", "e2e", "mock-backend", "cloud-proxy"),
		filepath.Join("tests", "e2e", "mock-backend", "cloud-proxy"),
		filepath.Join("..", "..", "tests", "e2e", "mock-backend", "cloud-proxy"),
	}
	for _, p := range cloudProxyCandidates {
		if _, err := os.Stat(p); err == nil {
			cloudProxyPath = p
			break
		}
	}
	if cloudProxyPath != "" {
		Expect(os.Chmod(cloudProxyPath, 0755)).To(Succeed())

		cpOpenAIUpstream = newFakeOpenAIUpstream()
		cpAnthropicUpstream = newFakeAnthropicUpstream()

		// API keys are read from env vars — set placeholder values so
		// the cloud-proxy backend's Load() doesn't fail with "unset".
		// The fake upstreams accept any auth header.
		Expect(os.Setenv("CLOUD_PROXY_E2E_OPENAI_KEY", "sk-e2e-openai")).To(Succeed())
		Expect(os.Setenv("CLOUD_PROXY_E2E_ANTHROPIC_KEY", "sk-ant-e2e")).To(Succeed())

		cloudProxyConfigs := []map[string]any{
			{
				"name":    "cp-passthrough-openai",
				"backend": "cloud-proxy",
				"parameters": map[string]any{
					"model": "cloud-proxy-passthrough-openai.bin",
				},
				"proxy": map[string]any{
					"mode":         "passthrough",
					"provider":     "openai",
					"upstream_url": cpOpenAIUpstream.URL() + "/v1/chat/completions",
					"api_key_env":  "CLOUD_PROXY_E2E_OPENAI_KEY",
				},
			},
			{
				"name":    "cp-passthrough-anthropic",
				"backend": "cloud-proxy",
				"parameters": map[string]any{
					"model": "cloud-proxy-passthrough-anthropic.bin",
				},
				"proxy": map[string]any{
					"mode":         "passthrough",
					"provider":     "anthropic",
					"upstream_url": cpAnthropicUpstream.URL() + "/v1/messages",
					"api_key_env":  "CLOUD_PROXY_E2E_ANTHROPIC_KEY",
				},
			},
			{
				"name":    "cp-translate-openai",
				"backend": "cloud-proxy",
				"parameters": map[string]any{
					"model": "cloud-proxy-translate-openai.bin",
				},
				"proxy": map[string]any{
					"mode":         "translate",
					"provider":     "openai",
					"upstream_url": cpOpenAIUpstream.URL() + "/v1/chat/completions",
					"api_key_env":  "CLOUD_PROXY_E2E_OPENAI_KEY",
				},
			},
			{
				"name":    "cp-translate-anthropic",
				"backend": "cloud-proxy",
				"parameters": map[string]any{
					"model": "cloud-proxy-translate-anthropic.bin",
				},
				"proxy": map[string]any{
					"mode":         "translate",
					"provider":     "anthropic",
					"upstream_url": cpAnthropicUpstream.URL() + "/v1/messages",
					"api_key_env":  "CLOUD_PROXY_E2E_ANTHROPIC_KEY",
				},
			},
		}
		for _, cfg := range cloudProxyConfigs {
			data, err := yaml.Marshal(cfg)
			Expect(err).ToNot(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(modelsPath, cfg["name"].(string)+".yaml"), data, 0644)).To(Succeed())
		}
	}

	systemState, err := system.GetSystemState(systemOpts...)
	Expect(err).ToNot(HaveOccurred())

	// Create application
	appCtx, appCancel = context.WithCancel(context.Background())

	// Create application instance (GeneratedContentDir so sound-generation/TTS can write files the handler sends)
	generatedDir := filepath.Join(tmpDir, "generated")
	Expect(os.MkdirAll(generatedDir, 0750)).To(Succeed())
	localAIApp, err = localaiapp.New(
		config.WithContext(appCtx),
		config.WithSystemState(systemState),
		config.WithDebug(true),
		config.WithGeneratedContentDir(generatedDir),
	)
	Expect(err).ToNot(HaveOccurred())

	// Register mock backend (always available for non-realtime tests).
	localAIApp.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)
	localAIApp.ModelLoader().SetExternalBackend("opus", mockBackendPath)
	if cloudProxyPath != "" {
		localAIApp.ModelLoader().SetExternalBackend("cloud-proxy", cloudProxyPath)
	}

	// Create HTTP app
	app, err = httpapi.API(localAIApp)
	Expect(err).ToNot(HaveOccurred())

	// Get free port
	port, err := freeport.GetFreePort()
	Expect(err).ToNot(HaveOccurred())
	apiPort = port
	apiURL = fmt.Sprintf("http://127.0.0.1:%d/v1", apiPort)
	// Anthropic SDK appends /v1/messages to base URL; use base without /v1 so requests go to /v1/messages
	anthropicBaseURL = fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	// Ollama client uses base URL directly
	ollamaBaseURL = fmt.Sprintf("http://127.0.0.1:%d", apiPort)

	// Start server in goroutine
	go func() {
		if err := app.Start(fmt.Sprintf("127.0.0.1:%d", apiPort)); err != nil && err != http.ErrServerClosed {
			xlog.Error("server error", "error", err)
		}
	}()

	// Wait for server to be ready
	client = openai.NewClient(option.WithBaseURL(apiURL))

	Eventually(func() error {
		_, err := client.Models.List(context.TODO())
		return err
	}, "2m").ShouldNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	// Synchronous shutdown — the context-cancel goroutine in application.New
	// runs the same cleanup asynchronously, which races test-binary exit and
	// orphans spawned mock-backend children to init.
	if localAIApp != nil {
		if err := localAIApp.Shutdown(); err != nil {
			xlog.Error("error shutting down application", "error", err)
		}
	}
	if appCancel != nil {
		appCancel()
	}
	if app != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(app.Shutdown(ctx)).To(Succeed())
	}
	if mcpServerShutdown != nil {
		mcpServerShutdown()
	}
	if cpOpenAIUpstream != nil {
		cpOpenAIUpstream.Close()
	}
	if cpAnthropicUpstream != nil {
		cpAnthropicUpstream.Close()
	}
	if tmpDir != "" {
		os.RemoveAll(tmpDir)
	}
})

func TestLocalAI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI E2E test suite")
}
