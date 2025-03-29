//go:build p2p
// +build p2p

package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	cliP2P "github.com/mudler/LocalAI/core/cli/p2p"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/pkg/assets"
	"github.com/mudler/LocalAI/pkg/library"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"
)

type P2P struct {
	WorkerFlags           `embed:""`
	cliP2P.P2PCommonFlags `embed:""`

	Token         string `env:"LOCALAI_TOKEN,LOCALAI_P2P_TOKEN,TOKEN" help:"P2P token to use"`
	NoRunner      bool   `env:"LOCALAI_NO_RUNNER,NO_RUNNER" help:"Do not start the llama-cpp-rpc-server"`
	RunnerAddress string `env:"LOCALAI_RUNNER_ADDRESS,RUNNER_ADDRESS" help:"Address of the llama-cpp-rpc-server"`
	RunnerPort    string `env:"LOCALAI_RUNNER_PORT,RUNNER_PORT" help:"Port of the llama-cpp-rpc-server"`
}

func (r *P2P) Run(ctx *cliContext.Context) error {
	// Extract files from the embedded FS
	err := assets.ExtractFiles(ctx.BackendAssets, r.BackendAssetsPath)
	log.Debug().Msgf("Extracting backend assets files to %s", r.BackendAssetsPath)
	if err != nil {
		log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly)", err)
	}

	// Check if the token is set
	// as we always need it.
	if r.Token == "" {
		return fmt.Errorf("Token is required")
	}
	p2pCfg := p2p.NewP2PConfig(r.P2PCommonFlags)
	p2pCfg.NetworkToken = r.Token

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

		_, err = p2p.ExposeService(context.Background(), p2pCfg, address, p, p2p.NetworkID(r.Peer2PeerNetworkID, p2p.WorkerID))
		if err != nil {
			return err
		}
		log.Info().Msgf("You need to start llama-cpp-rpc-server on '%s:%s'", address, p)
	} else {
		// Start llama.cpp directly from the version we have pre-packaged
		go func() {
			for {
				log.Info().Msgf("Starting llama-cpp-rpc-server on '%s:%d'", address, port)

				grpcProcess := assets.ResolvePath(
					r.BackendAssetsPath,
					"util",
					"llama-cpp-rpc-server",
				)
				var extraArgs []string

				if r.ExtraLLamaCPPArgs != "" {
					extraArgs = strings.Split(r.ExtraLLamaCPPArgs, " ")
				}
				args := append([]string{"--host", address, "--port", fmt.Sprint(port)}, extraArgs...)
				log.Debug().Msgf("Starting llama-cpp-rpc-server on '%s:%d' with args: %+v (%d)", address, port, args, len(args))

				args, grpcProcess = library.LoadLDSO(r.BackendAssetsPath, args, grpcProcess)

				cmd := exec.Command(
					grpcProcess, args...,
				)

				cmd.Env = os.Environ()

				cmd.Stderr = os.Stdout
				cmd.Stdout = os.Stdout

				if err := cmd.Start(); err != nil {
					log.Error().Any("grpcProcess", grpcProcess).Any("args", args).Err(err).Msg("Failed to start llama-cpp-rpc-server")
				}

				cmd.Wait()
			}
		}()

		_, err = p2p.ExposeService(context.Background(), p2pCfg, address, fmt.Sprint(port), p2p.NetworkID(r.Peer2PeerNetworkID, p2p.WorkerID))
		if err != nil {
			return err
		}
	}

	for {
		time.Sleep(1 * time.Second)
	}
}
