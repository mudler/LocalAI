package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	httpapi "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

func main() {
	mockBackend := flag.String("mock-backend", "", "path to mock-backend binary")
	port := flag.Int("port", 8089, "port to listen on")
	host := flag.String("host", "127.0.0.1", "host/address to bind on (use 0.0.0.0 to expose on the network)")
	// piiYAML lets a test inject a per-model `pii:` block into the
	// auto-generated mock-model.yaml. Used by the middleware end-to-end
	// verification (and any future test that wants to exercise per-model
	// gating without bringing up a real backend). The argument is the
	// body of the pii: block — the leading "pii:\n  " is added here.
	piiYAML := flag.String("pii-yaml", "", "optional pii: block to merge into mock-model.yaml")
	// extraModels accepts repeatable name=yaml pairs that get written
	// as additional model files. Used by the routing E2E to seed
	// candidate models a router model can dispatch to.
	extraModelFlag := flag.String("extra-model", "", "extra model YAML, formatted as 'name|<full yaml body>'. Repeatable via comma-then-pipe? — for the router test we ship a single big string with embedded newlines.")
	// routerYAML appends a `router:` block to mock-model.yaml. Used by
	// the routing E2E to turn mock-model into a smart-router that
	// dispatches to extra-models.
	routerYAML := flag.String("router-yaml", "", "optional router: block to merge into mock-model.yaml")
	flag.Parse()

	if *mockBackend == "" {
		fmt.Fprintln(os.Stderr, "error: --mock-backend is required")
		os.Exit(1)
	}

	// Resolve to absolute path
	absBackend, err := filepath.Abs(*mockBackend)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving mock-backend path: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(absBackend); err != nil {
		fmt.Fprintf(os.Stderr, "mock-backend not found at %s: %v\n", absBackend, err)
		os.Exit(1)
	}

	// Create temp dirs
	tmpDir, err := os.MkdirTemp("", "ui-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	modelsPath := filepath.Join(tmpDir, "models")
	backendsPath := filepath.Join(tmpDir, "backends")
	generatedDir := filepath.Join(tmpDir, "generated")
	dataDir := filepath.Join(tmpDir, "data")
	for _, d := range []string{modelsPath, backendsPath, generatedDir, dataDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating dir %s: %v\n", d, err)
			os.Exit(1)
		}
	}

	// Write mock-model config
	modelConfig := map[string]any{
		"name":    "mock-model",
		"backend": "mock-backend",
		"parameters": map[string]any{
			"model": "mock-model.bin",
		},
	}
	configYAML, err := yaml.Marshal(modelConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshaling config: %v\n", err)
		os.Exit(1)
	}
	body := configYAML
	if *piiYAML != "" {
		body = append(body, []byte("pii:\n  "+*piiYAML+"\n")...)
	}
	if *routerYAML != "" {
		body = append(body, []byte("router:\n  "+*routerYAML+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(modelsPath, "mock-model.yaml"), body, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		os.Exit(1)
	}

	if *extraModelFlag != "" {
		// extra-model format: "name|<yaml body>". The yaml body is
		// inlined verbatim — caller controls indentation. Single name
		// per flag invocation; multi-flag is fine because flag.String
		// only keeps the last but the test passes only one.
		parts := strings.SplitN(*extraModelFlag, "|", 2)
		if len(parts) == 2 {
			extraPath := filepath.Join(modelsPath, parts[0]+".yaml")
			if err := os.WriteFile(extraPath, []byte(parts[1]), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "error writing extra model: %v\n", err)
				os.Exit(1)
			}
		}
	}

	// Set up system state
	systemState, err := system.GetSystemState(
		system.WithModelPath(modelsPath),
		system.WithBackendPath(backendsPath),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting system state: %v\n", err)
		os.Exit(1)
	}

	// Create application
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app, err := application.New(
		config.WithContext(ctx),
		config.WithSystemState(systemState),
		config.WithDebug(true),
		config.WithDataPath(dataDir),
		config.WithDynamicConfigDir(dataDir),
		config.WithGeneratedContentDir(generatedDir),
		config.EnableTracing,
		config.EnableBackendLogging,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating application: %v\n", err)
		os.Exit(1)
	}

	// Register mock backend
	app.ModelLoader().SetExternalBackend("mock-backend", absBackend)

	// Create HTTP server
	e, err := httpapi.API(app)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating HTTP API: %v\n", err)
		os.Exit(1)
	}

	// Start server
	addr := fmt.Sprintf("%s:%d", *host, *port)
	go func() {
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			xlog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	fmt.Printf("UI test server listening on http://%s\n", addr)

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	cancel()
	e.Close()
}
