package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	"github.com/phayes/freeport"
)

const (
	mlxDistributedGalleryName = "mlx-distributed"
)

type P2PMLX struct {
	WorkerFlags        `embed:""`
	Token              string `env:"LOCALAI_TOKEN,LOCALAI_P2P_TOKEN,TOKEN" help:"P2P token to use"`
	Peer2PeerNetworkID string `env:"LOCALAI_P2P_NETWORK_ID,P2P_NETWORK_ID" help:"Network ID for P2P mode" group:"p2p"`
	MLXListenPort      string `env:"MLX_LISTEN_PORT" default:"5555" help:"Port for MLX distributed communication"`
	MLXBackend         string `env:"MLX_DISTRIBUTED_BACKEND" default:"ring" help:"MLX distributed backend (ring or jaccl)"`
}

func findMLXDistributedBackend(galleries string, systemState *system.SystemState) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		xlog.Warn("Failed listing system backends", "error", err)
		return "", err
	}

	backend, ok := backends.Get(mlxDistributedGalleryName)
	if !ok {
		ml := model.NewModelLoader(systemState)
		var gals []config.Gallery
		if err := json.Unmarshal([]byte(galleries), &gals); err != nil {
			xlog.Error("failed loading galleries", "error", err)
			return "", err
		}
		err := gallery.InstallBackendFromGallery(context.Background(), gals, systemState, ml, mlxDistributedGalleryName, nil, true)
		if err != nil {
			xlog.Error("mlx-distributed backend not found, failed to install it", "error", err)
			return "", err
		}
	}

	backendPath := filepath.Dir(backend.RunFile)
	if backendPath == "" {
		return "", errors.New("mlx-distributed backend not found, install it first")
	}

	return backendPath, nil
}

func (r *P2PMLX) Run(ctx *cliContext.Context) error {
	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return err
	}

	if r.Token == "" {
		return fmt.Errorf("Token is required")
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

	backendPath, err := findMLXDistributedBackend(r.BackendGalleries, systemState)
	if err != nil {
		xlog.Warn("Could not find mlx-distributed backend from gallery, will use backend.py directly", "error", err)
	}

	// Start the MLX worker process
	go func() {
		for {
			xlog.Info("Starting mlx-distributed worker", "address", address, "port", port)

			var cmd *exec.Cmd
			if backendPath != "" {
				cmd = exec.Command(
					filepath.Join(backendPath, "run.sh"),
					"--worker",
					"--backend", r.MLXBackend,
					"--hostfile", os.Getenv("MLX_DISTRIBUTED_HOSTFILE"),
					"--rank", "0", // Will be overridden by hostfile position
				)
			} else {
				cmd = exec.Command(
					"python3", "backend.py",
					"--worker",
					"--backend", r.MLXBackend,
					"--hostfile", os.Getenv("MLX_DISTRIBUTED_HOSTFILE"),
					"--rank", "0",
				)
			}

			cmd.Env = os.Environ()
			cmd.Stderr = os.Stderr
			cmd.Stdout = os.Stdout

			if err := cmd.Start(); err != nil {
				xlog.Error("Failed to start mlx-distributed worker", "error", err)
			}

			cmd.Wait()
			time.Sleep(2 * time.Second)
		}
	}()

	// Expose this worker on the p2p network
	_, err = p2p.ExposeService(c, address, fmt.Sprint(port), r.Token, p2p.NetworkID(r.Peer2PeerNetworkID, p2p.MLXWorkerID))
	if err != nil {
		return err
	}

	xlog.Info("MLX distributed worker registered on P2P network", "address", address, "port", port)

	signals.RegisterGracefulTerminationHandler(func() {
		cancel()
	})

	for {
		time.Sleep(1 * time.Second)
	}
}
