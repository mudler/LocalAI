package worker

import (
	"fmt"
	"os"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

type MLXDistributed struct {
	WorkerFlags `embed:""`
	Hostfile    string `env:"MLX_DISTRIBUTED_HOSTFILE" required:"" help:"Path to hostfile JSON. Ring: array of 'ip:port' where entry i is rank i's listen address. JACCL: 2D matrix of RDMA device names."`
	Rank        int    `env:"MLX_RANK" required:"" help:"Rank of this process (0 = gRPC server + ring participant, >0 = worker only)"`
	Backend     string `env:"MLX_DISTRIBUTED_BACKEND" default:"ring" help:"MLX distributed backend: 'ring' (TCP pipeline parallelism) or 'jaccl' (RDMA tensor parallelism)"`
	Addr        string `env:"MLX_DISTRIBUTED_ADDR" default:"localhost:50051" help:"gRPC API listen address for LocalAI (rank 0 only, separate from ring communication)"`
	Coordinator string `env:"MLX_JACCL_COORDINATOR" default:"" help:"JACCL coordinator ip:port — rank 0's address where it accepts RDMA setup connections (all ranks must use the same value)"`
}

func (r *MLXDistributed) Run(ctx *cliContext.Context) error {
	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return err
	}

	backendPath, err := findMLXDistributedBackendPath(r.BackendGalleries, systemState)
	if err != nil {
		return fmt.Errorf("cannot find mlx-distributed backend: %w", err)
	}

	args := []string{
		"--backend", r.Backend,
		"--hostfile", r.Hostfile,
		"--rank", fmt.Sprint(r.Rank),
	}

	if r.Rank == 0 {
		args = append(args, "--addr", r.Addr)
	} else {
		args = append(args, "--worker")
	}

	if r.Backend == "jaccl" && r.Coordinator != "" {
		args = append(args, "--coordinator", r.Coordinator)
	}

	cmd := buildMLXCommand(backendPath, args...)
	runSh := cmd.Path

	xlog.Info("Starting mlx-distributed", "rank", r.Rank, "backend", r.Backend, "hostfile", r.Hostfile)

	return syscall.Exec(
		runSh,
		append([]string{runSh}, args...),
		os.Environ(),
	)
}
