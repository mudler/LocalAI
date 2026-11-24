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
	WorkerFlags  `embed:""`
	P2PJoinFlags `embed:""`
	P2PStateDir  string `name:"p2p-state-dir" env:"LOCALAI_P2P_STATE_DIR" type:"path" default:"${basepath}/p2p/mlx-distributed" help:"Directory of the P2P identity, pairing and ring hostfile of this worker (with --join). Keep it across restarts: the server knows the worker by this identity" group:"p2p"`
	Hostfile     string `env:"MLX_DISTRIBUTED_HOSTFILE" help:"Path to hostfile JSON. Ring: array of 'ip:port' where entry i is rank i's listen address. JACCL: 2D matrix of RDMA device names. Required without --join"`
	Rank         int    `env:"MLX_RANK" default:"-1" help:"Rank of this process (0 = gRPC server + ring participant, >0 = worker only). Required without --join; with --join the server sets it"`
	Backend      string `env:"MLX_DISTRIBUTED_BACKEND" default:"ring" help:"MLX distributed backend: 'ring' (TCP pipeline parallelism) or 'jaccl' (RDMA tensor parallelism)"`
	Addr         string `env:"MLX_DISTRIBUTED_ADDR" default:"localhost:50051" help:"gRPC API listen address for LocalAI (rank 0 only, separate from ring communication)"`
	Coordinator  string `env:"MLX_JACCL_COORDINATOR" default:"" help:"JACCL coordinator ip:port — rank 0's address where it accepts RDMA setup connections (all ranks must use the same value)"`
}

func (r *MLXDistributed) Run(ctx *cliContext.Context) error {
	if err := requireHostBackendBuild("mlx-distributed", "mlx-distributed"); err != nil {
		return err
	}
	if r.Join != "" {
		return r.runP2P()
	}
	if r.Hostfile == "" || r.Rank < 0 {
		return fmt.Errorf("--hostfile and --rank are required, or --join <invite link> to join a server over P2P")
	}
	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
		system.WithDisableBackendInstall(r.DisableBackendInstall),
	)
	if err != nil {
		return err
	}

	backendPath, err := findMLXDistributedBackendPath(r.BackendGalleries, systemState, r.RequireBackendIntegrity)
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
