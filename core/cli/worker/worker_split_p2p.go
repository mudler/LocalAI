package worker

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2psplit"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/system"
)

// restartDelay is the pause before a child process that exited starts again.
const restartDelay = 2 * time.Second

// runP2P runs llama.cpp's rpc-server on a loopback port and lends it to the
// server of the invite over P2P.
func (r *LLamaCPP) runP2P() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
		system.WithDisableBackendInstall(r.DisableBackendInstall),
	)
	if err != nil {
		return err
	}
	rpcServer, err := findLLamaCPPBackend(r.BackendGalleries, systemState, r.RequireBackendIntegrity)
	if err != nil {
		return err
	}
	j, err := r.join(ctx, r.P2PStateDir, string(p2psplit.EngineLlama))
	if err != nil {
		return err
	}
	defer func() { _ = j.host.Close() }()

	port, err := freeLoopbackPort()
	if err != nil {
		return err
	}
	args := []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port)}
	if r.ExtraLLamaCPPArgs != "" {
		args = append(args, strings.Fields(r.ExtraLLamaCPPArgs)...)
	}
	go superviseProcess(ctx, "llama-cpp-rpc-server", func(ctx context.Context) *exec.Cmd {
		path, argv, env := llamaCPPRPCStart(runtime.GOOS, rpcServer, args, os.Environ())
		cmd := exec.CommandContext(ctx, path, argv[1:]...) // #nosec G204 -- the path is the installed backend binary and args are built from validated flags, with no shell
		cmd.Env = env
		return cmd
	})

	agent, err := p2psplit.NewAgent(p2psplit.AgentConfig{
		Host: j.host, Server: j.server, Dial: j.dial, Engine: p2psplit.EngineLlama,
		Name: j.name, Version: internal.Version, MemoryBytes: workerMemory(true),
		RPCAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	})
	if err != nil {
		return err
	}
	return agent.Run(ctx)
}

// runP2P runs an MLX ring worker for the server of the invite. The server
// gives the worker its rank; the worker (re)starts the MLX process each time
// the ring changes.
func (r *MLXDistributed) runP2P() error {
	if r.Backend != "ring" {
		return fmt.Errorf("--join supports the ring backend only: %s needs RDMA links between the machines", r.Backend)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	j, err := r.join(ctx, r.P2PStateDir, string(p2psplit.EngineMLX))
	if err != nil {
		return err
	}
	defer func() { _ = j.host.Close() }()

	assignments := make(chan p2psplit.Assignment, 1)
	go runMLXRanks(ctx, backendPath, assignments)
	agent, err := p2psplit.NewAgent(p2psplit.AgentConfig{
		Host: j.host, Server: j.server, Dial: j.dial, Engine: p2psplit.EngineMLX,
		Name: j.name, Version: internal.Version, MemoryBytes: workerMemory(false),
		RingDir: r.P2PStateDir,
		OnAssign: func(a p2psplit.Assignment) {
			// Only the latest assignment matters.
			select {
			case <-assignments:
			default:
			}
			assignments <- a
		},
	})
	if err != nil {
		return err
	}
	return agent.Run(ctx)
}

// runMLXRanks keeps one MLX worker process for the current assignment.
func runMLXRanks(ctx context.Context, backendPath string, assignments <-chan p2psplit.Assignment) {
	stop := func() {}
	defer func() { stop() }()
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-assignments:
			stop()
			stop = func() {}
			if a.Rank > 0 {
				stop = startMLXRank(ctx, backendPath, a)
			}
		}
	}
}

// startMLXRank starts the MLX worker process of one assignment and returns
// the function that stops it.
func startMLXRank(ctx context.Context, backendPath string, a p2psplit.Assignment) func() {
	pctx, cancel := context.WithCancel(ctx)
	args := []string{"--worker", "--backend", "ring", "--hostfile", a.Hostfile, "--rank", strconv.Itoa(a.Rank)}
	go superviseProcess(pctx, fmt.Sprintf("mlx-distributed rank %d", a.Rank), func(ctx context.Context) *exec.Cmd {
		cmd := buildMLXCommand(backendPath, args...)
		return exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...) // #nosec G204 -- the path is the installed backend binary and args are built here, with no shell
	})
	return cancel
}

// superviseProcess runs the command until ctx ends and starts it again when
// it exits.
func superviseProcess(ctx context.Context, name string, build func(context.Context) *exec.Cmd) {
	for ctx.Err() == nil {
		cmd := build(ctx)
		cmd.Env = os.Environ()
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		xlog.Info("Starting "+name, "args", cmd.Args[1:])
		if err := cmd.Run(); err != nil && ctx.Err() == nil {
			xlog.Error(name+" exited", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(restartDelay):
		}
	}
}

func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
