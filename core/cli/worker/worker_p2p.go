package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

				var extraArgs []string

				if r.ExtraLLamaCPPArgs != "" {
					extraArgs = strings.Split(r.ExtraLLamaCPPArgs, " ")
				}
				args := append([]string{"--host", address, "--port", fmt.Sprint(port)}, extraArgs...)
				log.Debug().Msgf("Starting llama-cpp-rpc-server on '%s:%d' with args: %+v (%d)", address, port, args, len(args))

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
