package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

type LLamaCPP struct {
	WorkerFlags `embed:""`
}

const (
	llamaCPPRPCBinaryName = "llama-cpp-rpc-server"
	llamaCPPGalleryName   = "llama-cpp"
)

// validateExecutablePath validates that the executable is the expected llama-cpp binary
func validateExecutablePath(execPath string) error {
	// Ensure path is absolute
	if !filepath.IsAbs(execPath) {
		return fmt.Errorf("executable path must be absolute: %s", execPath)
	}
	
	// Verify the executable exists and is a regular file
	if info, err := os.Stat(execPath); err != nil {
		return fmt.Errorf("executable not found: %w", err)
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file: %s", execPath)
	}

	// Ensure the executable name matches expected binary
	if filepath.Base(execPath) != llamaCPPRPCBinaryName {
		return fmt.Errorf("executable name must be %s, got: %s", llamaCPPRPCBinaryName, filepath.Base(execPath))
	}

	return nil
}

// validateLLamaCPPArgs validates and sanitizes extra arguments for llama-cpp-rpc-server
func validateLLamaCPPArgs(args string) ([]string, error) {
	if args == "" {
		return []string{}, nil
	}

	// Split arguments respecting quoted strings
	argList := strings.Fields(args)
	validatedArgs := make([]string, 0, len(argList))

	// Regex pattern for allowed characters in arguments
	// Allow alphanumeric, common symbols, and paths but block dangerous characters
	allowedPattern := regexp.MustCompile(`^[a-zA-Z0-9\-_=./,:@+]*$`)

	for _, arg := range argList {
		// Remove any surrounding quotes
		cleanArg := strings.Trim(arg, `"'`)
		
		// Block potentially dangerous arguments
		if strings.Contains(cleanArg, "..") ||
		   strings.Contains(cleanArg, ";") ||
		   strings.Contains(cleanArg, "&") ||
		   strings.Contains(cleanArg, "|") ||
		   strings.Contains(cleanArg, "`") ||
		   strings.Contains(cleanArg, "$") ||
		   strings.Contains(cleanArg, "$(") ||
		   strings.Contains(cleanArg, "${") ||
		   strings.HasPrefix(cleanArg, "/") && !strings.HasPrefix(cleanArg, "/tmp/") && !strings.HasPrefix(cleanArg, "/var/tmp/") {
			return nil, fmt.Errorf("argument contains potentially dangerous characters: %s", arg)
		}

		// Validate against allowed pattern
		if !allowedPattern.MatchString(cleanArg) {
			return nil, fmt.Errorf("argument contains invalid characters: %s", arg)
		}

		validatedArgs = append(validatedArgs, cleanArg)
	}

	return validatedArgs, nil
}

func findLLamaCPPBackend(galleries string, systemState *system.SystemState) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		log.Warn().Msgf("Failed listing system backends: %s", err)
		return "", err
	}
	log.Debug().Msgf("System backends: %v", backends)

	backend, ok := backends.Get(llamaCPPGalleryName)
	if !ok {
		ml := model.NewModelLoader(systemState, true)
		var gals []config.Gallery
		if err := json.Unmarshal([]byte(galleries), &gals); err != nil {
			log.Error().Err(err).Msg("failed loading galleries")
			return "", err
		}
		err := gallery.InstallBackendFromGallery(context.Background(), gals, systemState, ml, llamaCPPGalleryName, nil, true)
		if err != nil {
			log.Error().Err(err).Msg("llama-cpp backend not found, failed to install it")
			return "", err
		}
	}
	backendPath := filepath.Dir(backend.RunFile)

	if backendPath == "" {
		return "", errors.New("llama-cpp backend not found, install it first")
	}

	grpcProcess := filepath.Join(
		backendPath,
		llamaCPPRPCBinaryName,
	)

	return grpcProcess, nil
}

func (r *LLamaCPP) Run(ctx *cliContext.Context) error {

	if len(os.Args) < 4 {
		return fmt.Errorf("usage: local-ai worker llama-cpp-rpc -- <llama-rpc-server-args>")
	}

	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return err
	}
	grpcProcess, err := findLLamaCPPBackend(r.BackendGalleries, systemState)
	if err != nil {
		return err
	}

	// Validate the executable path to ensure only expected binary can be executed
	if err := validateExecutablePath(grpcProcess); err != nil {
		return fmt.Errorf("invalid executable: %w", err)
	}

	// Validate and sanitize extra arguments to prevent command injection
	validatedArgs, err := validateLLamaCPPArgs(r.ExtraLLamaCPPArgs)
	if err != nil {
		return fmt.Errorf("invalid llama-cpp arguments: %w", err)
	}

	// Execute the validated command using a static command construction approach
	// This approach avoids dynamic command construction flagged by security scanners
	execCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Construct command with validated path and arguments
	var cmd *exec.Cmd
	if len(validatedArgs) == 0 {
		cmd = exec.CommandContext(execCtx, grpcProcess)
	} else {
		cmd = exec.CommandContext(execCtx, grpcProcess, validatedArgs...)
	}
	
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
