package worker

import (
	"context"
	"fmt"
	"os"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	"github.com/phayes/freeport"
)

type P2PMLX struct {
	WorkerFlags        `embed:""`
	Token              string `env:"LOCALAI_TOKEN,LOCALAI_P2P_TOKEN,TOKEN" help:"P2P token to use"`
	Peer2PeerNetworkID string `env:"LOCALAI_P2P_NETWORK_ID,P2P_NETWORK_ID" help:"Network ID for P2P mode" group:"p2p"`
	MLXListenPort      string `env:"MLX_LISTEN_PORT" default:"5555" help:"Port for MLX distributed communication"`
	MLXBackend         string `env:"MLX_DISTRIBUTED_BACKEND" default:"ring" help:"MLX distributed backend (ring or jaccl)"`
}

func (r *P2PMLX) Run(ctx *cliContext.Context) error {
	if r.Token == "" {
		return fmt.Errorf("token is required")
	}

	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return err
	}

	port, err := freeport.GetFreePort()
	if err != nil {
		return err
	}
	if r.MLXListenPort != "" {
		fmt.Sscanf(r.MLXListenPort, "%d", &port)
	}

	address := "127.0.0.1"

	c, cancel := context.WithCancel(context.Background())
	defer cancel()

	backendPath, err := findMLXDistributedBackendPath(r.BackendGalleries, systemState)
	if err != nil {
		xlog.Warn("Could not find mlx-distributed backend from gallery, will try backend.py directly", "error", err)
	}

	go func() {
		for {
			hostfile := os.Getenv("MLX_DISTRIBUTED_HOSTFILE")
			if hostfile == "" {
				xlog.Info("Waiting for MLX_DISTRIBUTED_HOSTFILE to be set by P2P discovery...")
				time.Sleep(2 * time.Second)
				continue
			}

			xlog.Info("Starting mlx-distributed worker", "address", address, "port", port, "hostfile", hostfile)

			cmd := buildMLXCommand(backendPath,
				"--worker",
				"--backend", r.MLXBackend,
				"--hostfile", hostfile,
				"--rank", "0",
			)
			cmd.Env = os.Environ()
			cmd.Stderr = os.Stderr
			cmd.Stdout = os.Stdout

			if err := cmd.Run(); err != nil {
				xlog.Error("mlx-distributed worker exited", "error", err)
			}
			time.Sleep(2 * time.Second)
		}
	}()

	_, err = p2p.ExposeService(c, address, fmt.Sprint(port), r.Token, p2p.NetworkID(r.Peer2PeerNetworkID, p2p.MLXWorkerID))
	if err != nil {
		return err
	}

	xlog.Info("MLX distributed worker registered on P2P network", "address", address, "port", port)

	signals.RegisterGracefulTerminationHandler(func() {
		cancel()
	})

	<-c.Done()
	return nil
}
