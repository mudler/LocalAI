package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"
)

type P2P struct {
	WorkerFlags        `embed:""`
	Token              string `env:"LOCALAI_TOKEN,LOCALAI_P2P_TOKEN,TOKEN" help:"P2P token to use"`
	NoRunner           bool   `env:"LOCALAI_NO_RUNNER,NO_RUNNER" help:"Do not start the llama-cpp-rpc-server"`
	RunnerAddress      string `env:"LOCALAI_RUNNER_ADDRESS,RUNNER_ADDRESS" help:"Address of the llama-cpp-rpc-server"`
	RunnerPort         string `env:"LOCALAI_RUNNER_PORT,RUNNER_PORT" help:"Port of the llama-cpp-rpc-server"`
	Peer2PeerNetworkID string `env:"LOCALAI_P2P_NETWORK_ID,P2P_NETWORK_ID" help:"Network ID for P2P mode, can be set arbitrarly by the user for grouping a set of instances" group:"p2p"`
}

// validateLLamaCPPArgs validates and sanitizes arguments to prevent command injection
func validateLLamaCPPArgs(args string) ([]string, error) {
	if args == "" {
		return []string{}, nil
	}

	// Split arguments properly
	argList := strings.Fields(args)
	
	// Define allowed argument patterns for llama-cpp-rpc-server
	allowedArgPatterns := []string{
		"^--threads$", "^-t$", "^--threads=\\d+$",
		"^--ctx-size$", "^-c$", "^--ctx-size=\\d+$", 
		"^--batch-size$", "^-b$", "^--batch-size=\\d+$",
		"^--gpu-layers$", "^-ngl$", "^--gpu-layers=\\d+$",
		"^--main-gpu$", "^--main-gpu=\\d+$",
		"^--tensor-split$", "^--tensor-split=[\\d,\\.]+$",
		"^--low-vram$", "^--mmap$", "^--no-mmap$",
		"^--numa$", "^--verbose$", "^-v$",
		"^--seed$", "^--seed=\\d+$",
		"^--n-predict$", "^-n$", "^--n-predict=\\d+$",
		"^--temp$", "^--temp=[\\d\\.]+$",
		"^--top-k$", "^--top-k=\\d+$",
		"^--top-p$", "^--top-p=[\\d\\.]+$",
		"^--repeat-penalty$", "^--repeat-penalty=[\\d\\.]+$",
		"^--memory$", "^--memory=[\\d\\.]+[KMG]?$",
		"^--model$", "^-m$",
		"^[a-zA-Z0-9/._-]+$", // For model paths and simple values
	}

	// Check for dangerous characters that could enable command injection
	dangerousChars := regexp.MustCompile(`[;&|<>$(){}[\]'"\\\n\r\t]`)
	
	var validatedArgs []string
	for i, arg := range argList {
		// Check for dangerous characters
		if dangerousChars.MatchString(arg) {
			return nil, fmt.Errorf("argument contains dangerous characters: %s", arg)
		}
		
		// Validate against allowed patterns
		isValid := false
		for _, pattern := range allowedArgPatterns {
			matched, _ := regexp.MatchString(pattern, arg)
			if matched {
				isValid = true
				break
			}
		}
		
		// Special case: if this is a value for a previous flag, allow it if it's alphanumeric
		if !isValid && i > 0 {
			prevArg := argList[i-1]
			if (prevArg == "--model" || prevArg == "-m" || prevArg == "--threads" || prevArg == "-t" ||
				prevArg == "--ctx-size" || prevArg == "-c" || prevArg == "--batch-size" || prevArg == "-b" ||
				prevArg == "--gpu-layers" || prevArg == "-ngl" || prevArg == "--main-gpu" ||
				prevArg == "--seed" || prevArg == "--n-predict" || prevArg == "-n" ||
				prevArg == "--temp" || prevArg == "--top-k" || prevArg == "--top-p" ||
				prevArg == "--repeat-penalty" || prevArg == "--memory" || prevArg == "--tensor-split") {
				// Allow alphanumeric values with common safe characters
				if matched, _ := regexp.MatchString(`^[a-zA-Z0-9/._,-]+$`, arg); matched {
					isValid = true
				}
			}
		}
		
		if !isValid {
			return nil, fmt.Errorf("invalid or unsafe argument: %s", arg)
		}
		
		validatedArgs = append(validatedArgs, arg)
	}
	
	return validatedArgs, nil
}

// validateExecutablePath validates that the executable path is safe to prevent command injection
func validateExecutablePath(execPath string) error {
	if execPath == "" {
		return fmt.Errorf("executable path is empty")
	}

	// Check for dangerous characters that could enable command injection
	dangerousChars := regexp.MustCompile(`[;&|<>$(){}[\]'"\\\n\r\t]`)
	if dangerousChars.MatchString(execPath) {
		return fmt.Errorf("executable path contains dangerous characters: %s", execPath)
	}

	// Ensure the path only contains safe characters (alphanumeric, slash, dash, underscore, dot)
	safePathPattern := regexp.MustCompile(`^[a-zA-Z0-9/._-]+$`)
	if !safePathPattern.MatchString(execPath) {
		return fmt.Errorf("executable path contains invalid characters: %s", execPath)
	}

	// Ensure the executable name ends with the expected binary name
	if !strings.HasSuffix(execPath, "llama-cpp-rpc-server") {
		return fmt.Errorf("executable path does not end with expected binary name: %s", execPath)
	}

	return nil
}

// createSafeCommand creates a safe exec.Cmd with validated executable and arguments
func createSafeCommand(execPath string, args []string) *exec.Cmd {
	// Double-check the executable path validation
	if err := validateExecutablePath(execPath); err != nil {
		log.Error().Err(err).Msg("Invalid executable path in createSafeCommand")
		return nil
	}

	// Create the command with validated parameters
	cmd := exec.Command(execPath, args...)
	return cmd
}

func (r *P2P) Run(ctx *cliContext.Context) error {

	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return err
	}

	// Check if the token is set
	// as we always need it.
	if r.Token == "" {
		return fmt.Errorf("Token is required")
	}

	port, err := freeport.GetFreePort()
	if err != nil {
		return err
	}

	address := "127.0.0.1"

	c, cancel := context.WithCancel(context.Background())
	defer cancel()

	if r.NoRunner {
		// Let override which port and address to bind if the user
		// configure the llama-cpp service on its own
		p := fmt.Sprint(port)
		if r.RunnerAddress != "" {
			address = r.RunnerAddress
		}
		if r.RunnerPort != "" {
			p = r.RunnerPort
		}

		_, err = p2p.ExposeService(c, address, p, r.Token, p2p.NetworkID(r.Peer2PeerNetworkID, p2p.WorkerID))
		if err != nil {
			return err
		}
		log.Info().Msgf("You need to start llama-cpp-rpc-server on '%s:%s'", address, p)
	} else {
		// Start llama.cpp directly from the version we have pre-packaged
		go func() {
			for {
				log.Info().Msgf("Starting llama-cpp-rpc-server on '%s:%d'", address, port)

				grpcProcess, err := findLLamaCPPBackend(r.BackendGalleries, systemState)
				if err != nil {
					log.Error().Err(err).Msg("Failed to find llama-cpp-rpc-server")
					return
				}

				// Validate the executable path to prevent command injection
				if err := validateExecutablePath(grpcProcess); err != nil {
					log.Error().Err(err).Msg("Invalid executable path")
					return
				}

				var extraArgs []string

				if r.ExtraLLamaCPPArgs != "" {
					// Validate and sanitize extra arguments to prevent command injection
					sanitizedArgs, err := validateLLamaCPPArgs(r.ExtraLLamaCPPArgs)
					if err != nil {
						log.Error().Err(err).Msg("Invalid extra llama-cpp arguments provided")
						return
					}
					extraArgs = sanitizedArgs
				}
				args := append([]string{"--host", address, "--port", fmt.Sprint(port)}, extraArgs...)
				log.Debug().Msgf("Starting llama-cpp-rpc-server on '%s:%d' with args: %+v (%d)", address, port, args, len(args))

				// Create command with validated executable and arguments
				cmd := createSafeCommand(grpcProcess, args)
				if cmd == nil {
					log.Error().Msg("Failed to create safe command")
					return
				}

				cmd.Env = os.Environ()

				cmd.Stderr = os.Stdout
				cmd.Stdout = os.Stdout

				if err := cmd.Start(); err != nil {
					log.Error().Any("grpcProcess", grpcProcess).Any("args", args).Err(err).Msg("Failed to start llama-cpp-rpc-server")
				}

				cmd.Wait()
			}
		}()

		_, err = p2p.ExposeService(c, address, fmt.Sprint(port), r.Token, p2p.NetworkID(r.Peer2PeerNetworkID, p2p.WorkerID))
		if err != nil {
			return err
		}
	}

	signals.RegisterGracefulTerminationHandler(func() {
		cancel()
	})

	for {
		time.Sleep(1 * time.Second)
	}
}
