package worker

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// vLLMFollowerRoleLabel marks a node as a vLLM data-parallel follower.
// Operators scope regular models away from these nodes via inverse
// selectors like {"!node.role":"vllm-follower"}.
const vLLMFollowerRoleLabel = "vllm-follower"

// VLLMDistributed runs a vLLM follower process for multi-node
// data-parallel inference. The head runs LocalAI's existing single-
// node vLLM gRPC backend with engine_args.data_parallel_size > 1;
// followers run vanilla `vllm serve --headless ...` and speak ZMQ
// directly to the head.
//
// The follower is operator-launched (no NATS / SmartRouter placement
// in this iteration). When --register-to is set, the worker self-
// registers as an agent-type node so it shows up in the admin UI; a
// `node.role=vllm-follower` label discourages model placement on it.
type VLLMDistributed struct {
	WorkerFlags `embed:""`

	// Registration (optional). Without these the worker just runs vLLM
	// and exits — no UI visibility. With them set, the follower
	// registers as an agent-type node, heartbeats while vLLM is
	// running, and deregisters on shutdown.
	RegisterTo        string `env:"LOCALAI_REGISTER_TO" help:"Frontend URL for self-registration. Empty = no registration." group:"registration"`
	RegistrationToken string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	NodeName          string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to vllm-<hostname>)" group:"registration"`
	NodeLabels        string `env:"LOCALAI_NODE_LABELS" help:"Comma-separated key=value labels for this node (node.role=vllm-follower is always added)" group:"registration"`
	HeartbeatInterval string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`

	// vLLM data-parallel placement. The head must advertise the same
	// data_parallel_size / data_parallel_rpc_port via its engine_args;
	// followers use --master-addr / --master-port to find it.
	Model                 string   `arg:"" help:"HuggingFace model ID or local path (must match the head)"`
	DataParallelSize      int      `name:"data-parallel-size" env:"VLLM_DATA_PARALLEL_SIZE" required:"" help:"Total DP ranks across all nodes"`
	DataParallelSizeLocal int      `name:"data-parallel-size-local" env:"VLLM_DATA_PARALLEL_SIZE_LOCAL" required:"" help:"DP ranks on this node"`
	StartRank             int      `name:"start-rank" env:"VLLM_DATA_PARALLEL_START_RANK" required:"" help:"Starting DP rank for this node (>0 for followers)"`
	MasterAddr            string   `name:"master-addr" env:"VLLM_DP_MASTER_ADDR" required:"" help:"Head node IP/hostname for DP RPC handshake"`
	MasterPort            int      `name:"master-port" env:"VLLM_DP_MASTER_PORT" required:"" help:"Head node DP RPC port"`
	Headless              bool     `env:"VLLM_HEADLESS" default:"true" negatable:"" help:"Headless follower mode (no API server)"`
	ExtraArgs             []string `name:"vllm-arg" env:"VLLM_EXTRA_ARGS" help:"Additional CLI args passed verbatim to vllm serve (e.g. --tensor-parallel-size 2). May be repeated."`
}

func (r *VLLMDistributed) Run(ctx *cliContext.Context) error {
	// Rank 0 is the head: it must serve the OpenAI API. --headless
	// disables that, so the combination is operator error and would
	// silently produce a cluster that can't accept requests.
	if r.Headless && r.StartRank == 0 {
		return fmt.Errorf("--start-rank 0 (head) cannot be --headless; the head serves the API")
	}

	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return fmt.Errorf("getting system state: %w", err)
	}

	backendPath, err := findBackendPath("vllm", r.BackendGalleries, systemState)
	if err != nil {
		return fmt.Errorf("cannot find vllm backend: %w", err)
	}

	args := r.buildVLLMArgs()
	runSh := filepath.Join(backendPath, "run.sh")

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Self-register so the follower is visible in the admin UI. Done
	// before vLLM starts so an unreachable frontend fails fast rather
	// than after the GPU is already loaded.
	if r.RegisterTo != "" {
		regClient := &workerregistry.RegistrationClient{
			FrontendURL:       r.RegisterTo,
			RegistrationToken: r.RegistrationToken,
		}
		nodeID, _, regErr := regClient.RegisterWithRetry(context.Background(), r.registrationBody(), 10)
		if regErr != nil {
			return fmt.Errorf("registering with frontend: %w", regErr)
		}
		xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", r.RegisterTo, "role", "vllm-follower")

		heartbeatInterval, _ := time.ParseDuration(r.HeartbeatInterval)
		heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)
		go regClient.HeartbeatLoop(shutdownCtx, nodeID, heartbeatInterval, r.heartbeatBody)

		defer regClient.GracefulDeregister(nodeID)
	}

	xlog.Info("Starting vllm follower",
		"model", r.Model,
		"data-parallel-size", r.DataParallelSize,
		"data-parallel-size-local", r.DataParallelSizeLocal,
		"start-rank", r.StartRank,
		"master", fmt.Sprintf("%s:%d", r.MasterAddr, r.MasterPort),
	)

	cmd := exec.CommandContext(shutdownCtx, runSh, args...)
	// VLLM_DP_* env vars are belt-and-braces alongside the explicit
	// CLI flags — vLLM honours both (vllm/envs.py:142-148).
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("VLLM_DP_MASTER_IP=%s", r.MasterAddr),
		fmt.Sprintf("VLLM_DP_MASTER_PORT=%d", r.MasterPort),
		fmt.Sprintf("VLLM_DP_SIZE=%d", r.DataParallelSize),
		fmt.Sprintf("VLLM_DP_RANK=%d", r.StartRank),
		"VLLM_DP_RANK_LOCAL=0",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Forward INT/TERM to vLLM so it gets a chance to clean up its ZMQ
	// sockets. exec.CommandContext kills with SIGKILL on cancellation,
	// which we want as a fallback only.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting vllm: %w", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	for {
		select {
		case sig := <-sigCh:
			xlog.Info("Forwarding signal to vllm", "signal", sig)
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		case err := <-waitErr:
			return err
		}
	}
}

// buildVLLMArgs assembles the vLLM CLI argv. Factored out for unit
// testing — Run is hard to test without a real backend install.
func (r *VLLMDistributed) buildVLLMArgs() []string {
	args := []string{"serve", r.Model}
	if r.Headless {
		args = append(args, "--headless")
	}
	args = append(args,
		"--data-parallel-size", strconv.Itoa(r.DataParallelSize),
		"--data-parallel-size-local", strconv.Itoa(r.DataParallelSizeLocal),
		"--data-parallel-start-rank", strconv.Itoa(r.StartRank),
		"--data-parallel-address", r.MasterAddr,
		"--data-parallel-rpc-port", strconv.Itoa(r.MasterPort),
	)
	args = append(args, r.ExtraArgs...)
	return args
}

// registrationBody mirrors agent_worker.go's shape: agent-type nodes
// don't need an address, which fits a follower that doesn't host any
// LocalAI gRPC backends. The node.role label lets operators scope
// regular model placement away from followers.
func (r *VLLMDistributed) registrationBody() map[string]any {
	nodeName := r.NodeName
	if nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			nodeName = fmt.Sprintf("vllm-follower-%d", os.Getpid())
		} else {
			nodeName = "vllm-" + hostname
		}
	}

	totalVRAM, _ := xsysinfo.TotalAvailableVRAM()
	gpuVendor, _ := xsysinfo.DetectGPUVendor()

	body := map[string]any{
		"name":           nodeName,
		"node_type":      nodes.NodeTypeAgent,
		"total_vram":     totalVRAM,
		"available_vram": totalVRAM,
		"gpu_vendor":     gpuVendor,
	}
	if r.RegistrationToken != "" {
		body["token"] = r.RegistrationToken
	}

	labels := ParseNodeLabels(r.NodeLabels)
	labels["node.role"] = vLLMFollowerRoleLabel
	body["labels"] = labels
	return body
}

func (r *VLLMDistributed) heartbeatBody() map[string]any {
	body := map[string]any{}
	aggregate := xsysinfo.GetGPUAggregateInfo()
	if aggregate.TotalVRAM > 0 {
		body["available_vram"] = aggregate.FreeVRAM
	}
	return body
}
