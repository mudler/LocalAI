//go:build p2p
// +build p2p

package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	cliContext "github.com/go-skynet/LocalAI/core/cli/context"
	"github.com/go-skynet/LocalAI/core/p2p"
	"github.com/go-skynet/LocalAI/pkg/assets"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"
)

type P2P struct {
	WorkerFlags       `embed:""`
	Token             string   `env:"LOCALAI_TOKEN,TOKEN" help:"JSON list of galleries"`
	NoRunner          bool     `env:"LOCALAI_NO_RUNNER,NO_RUNNER" help:"Do not start the llama-cpp-rpc-server"`
	RunnerAddress     string   `env:"LOCALAI_RUNNER_ADDRESS,RUNNER_ADDRESS" help:"Address of the llama-cpp-rpc-server"`
	RunnerPort        string   `env:"LOCALAI_RUNNER_PORT,RUNNER_PORT" help:"Port of the llama-cpp-rpc-server"`
	ExtraLLamaCPPArgs []string `env:"LOCALAI_EXTRA_LLAMA_CPP_ARGS,EXTRA_LLAMA_CPP_ARGS" help:"Extra arguments to pass to llama-cpp-rpc-server"`
}

func (r *P2P) Run(ctx *cliContext.Context) error {
	// Extract files from the embedded FS
	err := assets.ExtractFiles(ctx.BackendAssets, r.BackendAssetsPath)
	log.Debug().Msgf("Extracting backend assets files to %s", r.BackendAssetsPath)
	if err != nil {
		log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly, like gpt4all)", err)
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

		err = p2p.BindLLamaCPPWorker(context.Background(), address, p, r.Token)
		if err != nil {
			return err
		}
		log.Info().Msgf("You need to start llama-cpp-rpc-server on '%s:%s'", address, p)

		return nil
	}

	// Start llama.cpp directly from the version we have pre-packaged
	go func() {
		for {
			log.Info().Msgf("Starting llama-cpp-rpc-server on '%s:%d'", address, port)
			cmd := exec.Command(
				assets.ResolvePath(
					r.BackendAssetsPath,
					"util",
					"llama-cpp-rpc-server",
				),
				append([]string{"--host", address, "--port", fmt.Sprint(port)}, r.ExtraLLamaCPPArgs...)...,
			)

			cmd.Env = os.Environ()

			cmd.Stderr = os.Stdout
			cmd.Stdout = os.Stdout

			if err := cmd.Start(); err != nil {
				log.Error().Err(err).Msg("Failed to start llama-cpp-rpc-server")
			}

			cmd.Wait()
		}
	}()

	err = p2p.BindLLamaCPPWorker(context.Background(), address, fmt.Sprint(port), r.Token)
	if err != nil {
		return err
	}

	for {
		time.Sleep(1 * time.Second)
	}
}
