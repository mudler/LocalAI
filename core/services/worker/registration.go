package worker

import (
	"cmp"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

// effectiveBasePort returns the port used as base for gRPC backend processes.
// Priority: Addr port → ServeAddr port → 50051
func (cfg *Config) effectiveBasePort() int {
	for _, addr := range []string{cfg.Addr, cfg.ServeAddr} {
		if addr != "" {
			if _, portStr, ok := strings.Cut(addr, ":"); ok {
				if p, _ := strconv.Atoi(portStr); p > 0 {
					return p
				}
			}
		}
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
	hostname, _ := os.Hostname()
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
	advHost, _, _ := strings.Cut(cfg.advertiseAddr(), ":")
	httpPort := cfg.effectiveBasePort() - 1
	return fmt.Sprintf("%s:%d", advHost, httpPort)
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
	totalVRAM, _ := xsysinfo.TotalAvailableVRAM()
	gpuVendor, _ := xsysinfo.DetectGPUVendor()

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
		"max_replicas_per_model": maxReplicas,
	}

	// If no GPU detected, report system RAM so the scheduler/UI has capacity info
	if totalVRAM == 0 {
		if ramInfo, err := xsysinfo.GetSystemRAMInfo(); err == nil {
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
		if ramInfo, err := xsysinfo.GetSystemRAMInfo(); err == nil {
			body["available_ram"] = ramInfo.Available
		}
	}
	return body
}
