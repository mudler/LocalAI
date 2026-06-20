package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// Hardware-driven model-config defaults.
//
// This sits alongside the other config overriders (ApplyInferenceDefaults for
// model families, guessDefaultsFromFile for GGUF/NGPULayers): they all
// heuristically fill ModelConfig values the user left unset. Hardware tuning is
// the same domain — "adjust the config from the device that will run it" — so
// it lives here rather than scattered into the backend or a separate package.
//
// The heuristics are parameterized on a GPU descriptor (not on direct
// detection) so they apply in both deployment shapes: SetDefaults passes the
// LocalGPU on a single host, and the distributed router passes the *selected
// node's* reported GPU before loading there (the frontend that loaded the
// config may have no GPU at all).

// GPU describes the device that will run a model.
type GPU struct {
	// Vendor is "nvidia", "amd", … (matches xsysinfo vendor constants).
	Vendor string
	// ComputeCapability is the NVIDIA compute capability as "major.minor"
	// (e.g. "12.1" for GB10 / DGX Spark). Empty for non-NVIDIA / unknown.
	ComputeCapability string
	// VRAM is total device memory in bytes (0 = unknown).
	VRAM uint64
}

// Physical batch (n_batch / n_ubatch) defaults.
const (
	// DefaultPhysicalBatch is the conservative default when no hardware-specific
	// tuning applies. core/backend.DefaultBatchSize references this (single source).
	DefaultPhysicalBatch = 512
	// BlackwellPhysicalBatch is the default on NVIDIA Blackwell consumer GPUs
	// (sm_12x: sm_120 RTX 50-series, sm_121 GB10 / DGX Spark). A larger physical
	// batch materially lifts MoE prefill there (per-expert GEMM tiles fill
	// better); measured on a GB10 with Qwen3-30B-A3B to saturate around 2048.
	BlackwellPhysicalBatch = 2048
)

// IsNVIDIABlackwell reports whether the GPU is in the NVIDIA Blackwell consumer
// family (sm_12x). Datacenter Blackwell (B100/B200/GB200, sm_100 / cc 10.0)
// reports a different compute capability and is intentionally not matched.
func (g GPU) IsNVIDIABlackwell() bool {
	maj, _ := parseComputeCapability(g.ComputeCapability)
	return maj >= 12
}

// PhysicalBatch returns the canonical physical batch (n_batch/n_ubatch) for the
// given hardware, used when the model config leaves batch unset.
func PhysicalBatch(g GPU) int {
	if g.IsNVIDIABlackwell() {
		return BlackwellPhysicalBatch
	}
	return DefaultPhysicalBatch
}

// IsManagedPhysicalBatch reports whether n is a value PhysicalBatch assigns.
// Callers that re-tune a value chosen by an upstream host (the distributed
// router correcting the frontend's guess) use this to avoid clobbering an
// explicit user batch such as 1024.
func IsManagedPhysicalBatch(n int) bool {
	return n == DefaultPhysicalBatch || n == BlackwellPhysicalBatch
}

// Parallel-slot (n_parallel) VRAM tiers. llama.cpp serializes requests at
// n_parallel=1 (the backend default) and only auto-enables continuous batching
// when n_parallel > 1 — so a single-slot default makes concurrent requests
// queue. We default a slot count by GPU size so multi-user serving works out of
// the box. With the backend's unified KV cache the slots SHARE the context
// budget, so more slots add concurrency without multiplying KV memory.
const (
	parallelSlotsVRAMHigh = uint64(32) << 30 // >=32 GiB -> 8 slots
	parallelSlotsVRAMMid  = uint64(8) << 30  // >=8 GiB  -> 4 slots
	parallelSlotsVRAMLow  = uint64(4) << 30  // >=4 GiB  -> 2 slots
)

// DefaultParallelSlots returns the n_parallel default for the given GPU. Returns
// 1 (no concurrency) when VRAM is unknown or too small, so we never change
// behavior on CPU-only / tiny devices.
func DefaultParallelSlots(g GPU) int {
	switch {
	case g.VRAM >= parallelSlotsVRAMHigh:
		return 8
	case g.VRAM >= parallelSlotsVRAMMid:
		return 4
	case g.VRAM >= parallelSlotsVRAMLow:
		return 2
	default:
		return 1
	}
}

// EnsureParallelOption appends a VRAM-scaled "parallel:N" backend option when the
// model doesn't already set one (and the GPU warrants concurrency). Returns the
// possibly-extended options. Shared by the single-host config path
// (ApplyHardwareDefaults) and the distributed router (per selected node).
func EnsureParallelOption(opts []string, gpu GPU) []string {
	if slots := DefaultParallelSlots(gpu); slots > 1 && !hasParallelOption(opts) {
		return append(opts, fmt.Sprintf("parallel:%d", slots))
	}
	return opts
}

// hasParallelOption reports whether the model already sets parallel/n_parallel
// so we never override an explicit value (helper shared with serving_defaults.go).
func hasParallelOption(opts []string) bool {
	return backendOptionSet(opts, "parallel", "n_parallel")
}

// localGPU builds a GPU descriptor from local detection, used by SetDefaults on
// a single host (the distributed router builds it from the selected node's
// reported info instead). It is a package var so tests can inject a
// deterministic device — detection does a live nvidia-smi call.
var localGPU = func() GPU {
	vendor, _ := xsysinfo.DetectGPUVendor()
	vram, _ := xsysinfo.TotalAvailableVRAM()
	return GPU{
		Vendor:            vendor,
		ComputeCapability: xsysinfo.NVIDIAComputeCapability(),
		VRAM:              vram,
	}
}

// ApplyHardwareDefaults fills ModelConfig values that depend on the target GPU
// and were left unset by the user. Currently: a larger physical batch on
// Blackwell. Explicit config always wins (we only touch zero values).
func ApplyHardwareDefaults(cfg *ModelConfig, gpu GPU) {
	if cfg == nil {
		return
	}
	if cfg.Batch == 0 && gpu.IsNVIDIABlackwell() {
		cfg.Batch = BlackwellPhysicalBatch
		xlog.Debug("[hardware_defaults] Blackwell GPU: defaulting physical batch",
			"batch", cfg.Batch, "compute_cap", gpu.ComputeCapability)
	}

	// Enable concurrent serving by default on a capable GPU: without this the
	// llama.cpp backend runs n_parallel=1 and serializes multi-user requests
	// (continuous batching stays off). Unified KV means the slots share the
	// context budget, so this is concurrency without extra KV memory. Explicit
	// parallel/n_parallel in the model options always wins.
	if before := len(cfg.Options); true {
		cfg.Options = EnsureParallelOption(cfg.Options, gpu)
		if len(cfg.Options) > before {
			xlog.Debug("[hardware_defaults] defaulting parallel slots for concurrent serving",
				"option", cfg.Options[len(cfg.Options)-1], "vram_gib", gpu.VRAM>>30)
		}
	}
}

// parseComputeCapability splits a "major.minor" string into integer parts.
// Returns (-1, -1) when it can't be parsed.
func parseComputeCapability(cc string) (int, int) {
	cc = strings.TrimSpace(cc)
	if cc == "" {
		return -1, -1
	}
	majStr, minStr := cc, "0"
	if dot := strings.IndexByte(cc, '.'); dot >= 0 {
		majStr, minStr = cc[:dot], cc[dot+1:]
	}
	maj, err := strconv.Atoi(strings.TrimSpace(majStr))
	if err != nil {
		return -1, -1
	}
	min, err := strconv.Atoi(strings.TrimSpace(minStr))
	if err != nil {
		min = 0
	}
	return maj, min
}
