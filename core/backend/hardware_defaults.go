package backend

// Hardware-specific backend defaults.
//
// This file centralizes tuning that depends on the *detected hardware* rather
// than on the model config. The model config (explicit `batch:`, `context_size:`
// …) always takes precedence; these helpers only fill values the user left
// unset, so behavior is unchanged unless the matching hardware is present.
//
// Placement note: this runs in the process that builds the gRPC ModelOptions
// sent to every backend (including the C++ llama.cpp grpc-server), so it is the
// one common point that covers all backends. For distributed setups where the
// backend runs on a different host than the orchestrator, worker-side detection
// (e.g. the C++ backend reading cudaGetDeviceProperties) would be more precise;
// this single-host default is the pragmatic common case.

import (
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// BlackwellBatchSize is the physical batch (n_batch/n_ubatch) default on NVIDIA
// Blackwell consumer GPUs (sm_120/121, incl. GB10 / DGX Spark). A larger
// physical batch materially lifts MoE prefill throughput there (per-expert GEMM
// tiles fill better); measured on a GB10 with Qwen3-30B-A3B to lift the prefill
// ceiling ~+10-15% and saturate around 2048. Only applied when the model config
// does not set an explicit `batch:`.
const BlackwellBatchSize = 2048

// detectBlackwellGPU is a seam over xsysinfo.IsNVIDIABlackwell so tests can
// force the hardware branch deterministically.
var detectBlackwellGPU = xsysinfo.IsNVIDIABlackwell

// hardwareDefaultBatchSize returns the physical-batch default for the detected
// hardware, falling back to the given value when no hardware-specific tuning
// applies. Used by EffectiveBatchSize only when the config leaves batch unset.
func hardwareDefaultBatchSize(fallback int) int {
	if detectBlackwellGPU() {
		xlog.Debug("Blackwell GPU detected; defaulting physical batch higher for MoE prefill", "batch", BlackwellBatchSize)
		return BlackwellBatchSize
	}
	return fallback
}
