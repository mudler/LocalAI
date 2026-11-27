// SPDX-License-Identifier: MIT
package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	gguf "github.com/gpustack/gguf-parser-go"
)

// Hybrid and recurrent models (Qwen3.5 gated delta-net, Mamba2 hybrids such
// as Granite-H and Nemotron-H, Jamba, LFM2) keep a recurrent state per
// sequence, in f32, next to the KV cache. llama.cpp makes context checkpoints
// of that state so a prompt can be reused from an earlier position: every
// checkpoint is one more copy of the state, in host memory, and a slot keeps
// up to n_ctx_checkpoints of them. With several slots that reaches tens of GiB
// on the larger hybrids, so the defaults below bound it.

// recurrentStateBytes returns the recurrent state of one sequence of the model
// in f, the size of one context checkpoint, or 0 for a model without one (or
// whose metadata it does not understand). It follows llama_hparams n_embd_r
// and n_embd_s:
//
//	Mamba, Mamba2 and gated delta-net: R = (d_conv - 1) x (d_inner + 2 x n_group x d_state), S = d_state x d_inner
//	LFM2 short convolution:            R = n_embd x (l_cache - 1), S = 0
//
// per recurrent layer, 4 bytes per value.
func recurrentStateBytes(f *gguf.GGUFFile) (n uint64) {
	defer func() {
		if recover() != nil {
			n = 0
		}
	}()
	if f == nil {
		return 0
	}
	arch := rawGGUFArchitecture(f)
	if arch == "" {
		return 0
	}
	num := func(key string) uint64 {
		v, ok := f.Header.MetadataKV.Get(arch + "." + key)
		if !ok || v.ValueType == gguf.GGUFMetadataValueTypeArray {
			return 0
		}
		return gguf.ValueNumeric[uint64](v)
	}
	layers := num("block_count")
	if layers == 0 {
		return 0
	}

	var perLayer uint64
	if lCache := num("shortconv.l_cache"); lCache > 1 {
		perLayer = num("embedding_length") * (lCache - 1)
	} else if dConv, dInner, dState := num("ssm.conv_kernel"), num("ssm.inner_size"), num("ssm.state_size"); dConv > 1 && dInner > 0 && dState > 0 {
		nGroup := num("ssm.group_count")
		perLayer = (dConv-1)*(dInner+2*nGroup*dState) + dState*dInner
	}
	if perLayer == 0 {
		return 0
	}
	return recurrentLayers(f, arch, layers) * perLayer * 4
}

// recurrentLayers counts the layers that hold a recurrent state: all but every
// full_attention_interval-th layer (Qwen3.5), else the layers without KV heads
// in the per-layer head_count_kv array, else every layer (a purely recurrent
// model). Nemotron-H also has feed-forward-only layers, which have neither.
func recurrentLayers(f *gguf.GGUFFile, arch string, layers uint64) uint64 {
	if v, ok := f.Header.MetadataKV.Get(arch + ".full_attention_interval"); ok && v.ValueType != gguf.GGUFMetadataValueTypeArray {
		if k := gguf.ValueNumeric[uint64](v); k > 0 {
			return layers - layers/k
		}
	}
	kv, ok := f.Header.MetadataKV.Get(arch + ".attention.head_count_kv")
	if !ok || kv.ValueType != gguf.GGUFMetadataValueTypeArray {
		return layers
	}
	heads := gguf.ValuesNumeric[uint64](kv.ValueArray())
	var ff []uint64
	if strings.HasPrefix(arch, "nemotron_h") {
		if v, ok := f.Header.MetadataKV.Get(arch + ".feed_forward_length"); ok && v.ValueType == gguf.GGUFMetadataValueTypeArray {
			ff = gguf.ValuesNumeric[uint64](v.ValueArray())
		}
	}
	var n uint64
	for i, h := range heads {
		if h == 0 && (i >= len(ff) || ff[i] == 0) {
			n++
		}
	}
	return n
}

const (
	// paged serving's slot count when parallel is not set (the backend's
	// serving_policy::paged_default_parallel)
	backendDefaultParallel = 8
	// llama.cpp's default cache_ram, in MiB
	defaultCacheRAMMiB = 8192
	// LocalAI's default n_ctx_checkpoints, and the floor of the budget below
	maxCtxCheckpoints = 32
	minCtxCheckpoints = 4
)

// CheckpointsForBudget is the n_ctx_checkpoints that keeps the context
// checkpoints of every slot within budget bytes: budget / (slots x state),
// between 4 and 32. Four checkpoints still cover the last two turns of a
// chat (llama.cpp makes about three per turn), which is what a linear chat
// reuses.
func CheckpointsForBudget(budget, state uint64, slots int) int {
	if state == 0 || slots <= 0 {
		return maxCtxCheckpoints
	}
	n := budget / (uint64(slots) * state)
	return int(min(max(n, minCtxCheckpoints), maxCtxCheckpoints))
}

// checkpointBudget is the host memory the checkpoints of one model may use:
// the RAM prompt cache size (cache_ram, 8 GiB by default) or a quarter of the
// host memory, whichever is smaller. hostRAM 0 (unknown) leaves cache_ram.
func checkpointBudget(opts []string, hostRAM uint64) uint64 {
	mib := uint64(defaultCacheRAMMiB)
	if v, ok := backendOptionValue(opts, "cache_ram", "cache_ram_mib"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			mib = uint64(n)
		}
	}
	b := mib << 20
	if hostRAM > 0 {
		b = min(b, hostRAM/4)
	}
	return b
}

// EnsureCheckpointOption appends "n_ctx_checkpoints:N" for a model with a
// recurrent state when the model does not set it and the default of 32 per
// slot would not fit the checkpoint budget. slots is the slot count the
// backend will use.
func EnsureCheckpointOption(opts []string, state uint64, slots int, hostRAM uint64) []string {
	if state == 0 || backendOptionSet(opts, "n_ctx_checkpoints", "ctx_checkpoints") {
		return opts
	}
	n := CheckpointsForBudget(checkpointBudget(opts, hostRAM), state, slots)
	if n >= maxCtxCheckpoints {
		return opts
	}
	return append(opts, fmt.Sprintf("n_ctx_checkpoints:%d", n))
}

// backendOptionValue returns the value of the first of names set in opts.
func backendOptionValue(opts []string, names ...string) (string, bool) {
	for _, o := range opts {
		name, value, _ := strings.Cut(o, ":")
		if slices.Contains(names, strings.TrimSpace(strings.ToLower(name))) {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// slotsFromOptions is the slot count the llama.cpp backend will use: the
// parallel option when set, else paged serving's default.
func slotsFromOptions(opts []string) int {
	if v, ok := backendOptionValue(opts, "parallel", "n_parallel"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return backendDefaultParallel
}

// SlotsFromOptions is the slot count the llama.cpp backend will use for opts:
// the parallel option when set, else paged serving's default of 8.
func SlotsFromOptions(opts []string) int { return slotsFromOptions(opts) }

// RecurrentStateForModelFile returns the per-sequence recurrent state of the
// GGUF at path (0 for a model without one, or when the header cannot be
// read). The header comes from the same cache as the model defaults, so a
// file already seen is not parsed again.
func RecurrentStateForModelFile(path string) uint64 {
	if path == "" {
		return 0
	}
	h, err := loadGGUFHeader(path, gguf.ParseGGUFFile)
	if err != nil {
		return 0
	}
	return h.RecurrentStateBytes
}
