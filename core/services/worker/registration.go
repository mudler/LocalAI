package worker

import (
	"cmp"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// effectiveBasePort returns the port used as base for gRPC backend processes.
// Priority: Addr port → ServeAddr port → 50051
func (cfg *Config) effectiveBasePort() int {
	for _, addr := range []string{cfg.Addr, cfg.ServeAddr} {
		if addr == "" {
			continue
		}
		_, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			xlog.Warn("Invalid worker address; trying the next base-port source", "addr", addr, "error", err)
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			xlog.Warn("Invalid worker port; trying the next base-port source", "addr", addr, "port", portStr, "error", err)
			continue
		}
		if port > 0 && port <= 65535 {
			return port
		}
		xlog.Warn("Worker port is outside the valid range; trying the next base-port source", "addr", addr, "port", port)
	}
	return 50051
}

// advertiseAddr returns the address the frontend should use to reach this node.
func (cfg *Config) advertiseAddr() string {
	if cfg.AdvertiseAddr != "" {
		return cfg.AdvertiseAddr
	}
	if cfg.Addr != "" {
		return cfg.Addr
	}
	hostname, err := os.Hostname()
	if err != nil {
		xlog.Warn("Failed to determine worker hostname; advertising localhost", "error", err)
	}
	return fmt.Sprintf("%s:%d", cmp.Or(hostname, "localhost"), cfg.effectiveBasePort())
}

// resolveHTTPAddr returns the address to bind the HTTP file transfer server to.
// Uses basePort-1 so it doesn't conflict with dynamically allocated gRPC ports
// which grow upward from basePort.
func (cfg *Config) resolveHTTPAddr() string {
	if cfg.HTTPAddr != "" {
		return cfg.HTTPAddr
	}
	return fmt.Sprintf("0.0.0.0:%d", cfg.effectiveBasePort()-1)
}

// advertiseHTTPAddr returns the HTTP address the frontend should use to reach
// this node for file transfer.
func (cfg *Config) advertiseHTTPAddr() string {
	if cfg.AdvertiseHTTPAddr != "" {
		return cfg.AdvertiseHTTPAddr
	}
	advertiseAddr := cfg.advertiseAddr()
	advHost, _, err := net.SplitHostPort(advertiseAddr)
	if err != nil {
		xlog.Warn("Invalid worker advertise address; advertising file transfer on localhost", "addr", advertiseAddr, "error", err)
		advHost = "localhost"
	}
	httpPort := cfg.effectiveBasePort() - 1
	return net.JoinHostPort(advHost, strconv.Itoa(httpPort))
}

// registrationBody builds the JSON body for node registration.
func (cfg *Config) registrationBody() map[string]any {
	nodeName := cfg.NodeName
	if nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			nodeName = fmt.Sprintf("node-%d", os.Getpid())
		} else {
			nodeName = hostname
		}
	}

	// Detect GPU info for VRAM-aware scheduling
	totalVRAM, err := xsysinfo.TotalAvailableVRAM()
	if err != nil {
		xlog.Debug("Failed to detect worker VRAM; registering without GPU capacity", "error", err)
	}
	gpuVendor, err := xsysinfo.DetectGPUVendor()
	if err != nil {
		xlog.Debug("Failed to detect worker GPU vendor; registering without vendor metadata", "error", err)
	}
	// Compute capability (e.g. "12.1" for GB10) lets the router pick per-arch
	// options (e.g. larger physical batch on Blackwell). Detected on the worker
	// because only the worker sees the GPU in distributed mode.
	gpuComputeCap := xsysinfo.NVIDIAComputeCapability()
	// Report our own meta-backend capability so the controller can list the
	// backends this cluster can actually run. The controller cannot infer it
	// from the GPU vendor alone: OS-dependent capabilities (metal, darwin-x86,
	// nvidia-l4t) and the CUDA runtime refinements are only observable here.
	capability := ""
	if systemState, err := system.GetSystemState(); err != nil {
		xlog.Warn("Could not detect system capability for node registration", "error", err)
	} else {
		capability = systemState.DetectedCapability()
	}

	maxReplicas := cfg.MaxReplicasPerModel
	if maxReplicas < 1 {
		maxReplicas = 1
	}
	body := map[string]any{
		"name":                   nodeName,
		"address":                cfg.advertiseAddr(),
		"http_address":           cfg.advertiseHTTPAddr(),
		"total_vram":             totalVRAM,
		"available_vram":         totalVRAM, // initially all VRAM is available
		"gpu_vendor":             gpuVendor,
		"gpu_compute_capability": gpuComputeCap,
		"capability":             capability,
		"max_replicas_per_model": maxReplicas,
	}

	// Report the operator-set budget as a STRING so the server resolves and
	// enforces it against the raw VRAM above. The worker never caps its own
	// total_vram/available_vram, and never touches the xsysinfo process-global
	// budget (that is standalone-only). Omit when unset.
	if cfg.VRAMBudget != "" {
		body["vram_budget"] = cfg.VRAMBudget
	}

	// If no GPU detected, report system RAM so the scheduler/UI has capacity info
	if totalVRAM == 0 {
		ramInfo, err := xsysinfo.GetSystemRAMInfo()
		if err != nil {
			xlog.Debug("Failed to detect worker RAM for registration", "error", err)
		} else {
			body["total_ram"] = ramInfo.Total
			body["available_ram"] = ramInfo.Available
		}
	}
	if cfg.RegistrationToken != "" {
		body["token"] = cfg.RegistrationToken
	}

	// Parse and add static node labels. Always include the auto-label
	// `node.replica-slots=N` so AND-selectors in ModelSchedulingConfig can
	// target high-capacity nodes (e.g. {"node.replica-slots":"4"}).
	labels := make(map[string]string)
	if cfg.NodeLabels != "" {
		for _, pair := range strings.Split(cfg.NodeLabels, ",") {
			pair = strings.TrimSpace(pair)
			if k, v, ok := strings.Cut(pair, "="); ok {
				labels[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	labels["node.replica-slots"] = strconv.Itoa(maxReplicas)
	body["labels"] = labels

	return body
}

// heartbeatBody returns the current VRAM/RAM stats for heartbeat payloads.
//
// When aggregate VRAM usage is unknown (no GPU, or temporary detection
// failure), we deliberately OMIT available_vram so the frontend keeps its
// last good value — overwriting with 0 makes the UI show the node as "fully
// used", while reporting total-as-available lies to the scheduler about
// free capacity.
func (cfg *Config) heartbeatBody() map[string]any {
	body := map[string]any{}
	aggregate := xsysinfo.GetGPUAggregateInfo()
	if aggregate.TotalVRAM > 0 {
		body["available_vram"] = aggregate.FreeVRAM
	}

	// CPU-only workers (or workers that lost GPU visibility momentarily):
	// report system RAM so the scheduler still has capacity info.
	if aggregate.TotalVRAM == 0 {
		ramInfo, err := xsysinfo.GetSystemRAMInfo()
		if err != nil {
			xlog.Debug("Failed to detect worker RAM for heartbeat", "error", err)
		} else {
			body["available_ram"] = ramInfo.Available
		}
	}
	return body
}
