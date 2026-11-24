// Package llamacpp is the llama.cpp engine of enginesettings: the
// llama-server flags LocalAI maps to model config keys, the --model and
// --load-mode flags, the presets, and the "-ot" tensor placement helper.
package llamacpp

import (
	"path"
	"strings"

	es "github.com/mudler/LocalAI/pkg/enginesettings"
)

// ID is the id of the llama.cpp engine.
const ID = "llama-cpp"

// Binary is the program a built command starts with.
const Binary = "llama-server"

// loadModes lists the values accepted by --load-mode.
var loadModes = []string{"none", "mmap", "mlock", "mmap_mlock", "direct_io", "auto"}

func ignored(reason string, noValue bool, cli ...string) es.IgnoredFlag {
	return es.IgnoredFlag{CLI: cli, Reason: reason, NoValue: noValue}
}

// ignoredFlags are flags LocalAI sets itself or that would stop the server.
var ignoredFlags = []es.IgnoredFlag{
	ignored("LocalAI starts and addresses the backend process itself", false, "--host", "--port"),
	ignored("LocalAI handles API keys itself", false, "--api-key", "--api-key-file"),
	ignored("the model name is the name of the LocalAI model config", false, "-a", "--alias"),
	ignored("download the model with the gallery or the model import instead", false, "-hf", "--hf-repo", "-hfr", "-mu", "--model-url"),
	ignored("prints help and exits", true, "-h", "--help", "--usage"),
	ignored("prints the version and exits", true, "--version"),
	ignored("prints the license and exits", true, "--license"),
	ignored("prints the devices and exits", true, "--list-devices"),
	ignored("prints the cache and exits", true, "-cl", "--cache-list"),
}

// modelFlag sets parameters.model, the model file.
var modelFlag = es.SpecialFlag{
	CLI:   []string{"-m", "--model"},
	Paths: map[string]es.ValueType{"parameters.model": es.TypeString},
	Parse: func(p *es.ParseState, flag, v string, ok bool) {
		if !ok || v == "" {
			p.Unmap(flag, "", "missing value")
			return
		}
		p.Set("parameters.model", v)
		if strings.ContainsAny(v, `/\`) {
			p.Warn("the model %q is a path; LocalAI expects a file name inside the models directory", v)
		}
	},
	Build: func(b *es.BuildState) []string {
		if m := b.String("parameters.model"); m != "" {
			return []string{"-m", m}
		}
		return nil
	},
}

// loadModeFlag maps --load-mode onto mmap, mlock and direct_io. It sets all
// three, so the last of several flags wins.
var loadModeFlag = es.SpecialFlag{
	CLI:    []string{"--load-mode"},
	Covers: []string{"mmap", "mmlock", "direct_io"},
	Parse: func(p *es.ParseState, flag, v string, ok bool) {
		if !ok {
			p.Unmap(flag, "", "missing value")
			return
		}
		mode := strings.ToLower(v)
		mm, ml := new(bool), new(bool)
		dio := false
		switch strings.ReplaceAll(mode, "-", "_") {
		case "none":
		case "mmap":
			*mm = true
		case "mlock":
			*ml = true
		case "mmap_mlock", "mmap+mlock":
			*mm, *ml = true, true
		case "direct_io":
			dio = true
			mm, ml = nil, nil
		case "auto":
			p.Warn("--load-mode auto is not a LocalAI setting; mmap and mlock stay unset so the LocalAI default applies")
			mm, ml = nil, nil
		default:
			p.Unmap(flag, mode, "unknown load mode; expected one of "+strings.Join(loadModes, ", "))
			return
		}
		if mm != nil {
			p.Set("mmap", *mm)
		} else {
			p.Unset("mmap")
		}
		if ml != nil && *ml {
			p.Set("mmlock", true)
		} else {
			p.Unset("mmlock")
		}
		if dio {
			p.SetOption("direct_io", "direct_io:true")
		} else {
			p.UnsetOption("direct_io")
		}
	},
	Build: func(b *es.BuildState) []string {
		mmap, mlock := b.Bool("mmap"), b.Bool("mmlock")
		d, _ := b.Option("direct_io")
		dio := d == "true"
		if mmap == nil && mlock == nil && !dio {
			return nil
		}
		mm := mmap != nil && *mmap
		ml := mlock != nil && *mlock
		mode := "none"
		switch {
		case dio:
			mode = "direct_io"
		case mm && ml:
			mode = "mmap_mlock"
		case ml:
			mode = "mlock"
		case mm:
			mode = "mmap"
		}
		return []string{"--load-mode", mode}
	},
}

// loadMode is the combined control of the editor that matches --load-mode.
var loadMode = es.Combined{
	ID: "load_mode", Flag: "--load-mode", Group: GroupMemory,
	Doc:   "How the model file is read. Writes mmap, mmlock and the direct_io option.",
	Keys:  []string{"mmap", "mmlock", "direct_io"},
	Unset: map[string]any{"mmap": true, "mmlock": false, "direct_io": false},
	Choices: []es.Choice{
		{Value: "default", Label: "Default (memory-mapped)", Set: map[string]any{"mmap": nil, "mmlock": nil, "direct_io": nil}},
		{Value: "none", Label: "None (read into memory)", Set: map[string]any{"mmap": false, "mmlock": nil, "direct_io": nil}},
		{Value: "mmap", Label: "mmap", Set: map[string]any{"mmap": true, "mmlock": nil, "direct_io": nil}},
		{Value: "mlock", Label: "mlock", Set: map[string]any{"mmap": false, "mmlock": true, "direct_io": nil}},
		{Value: "mmap_mlock", Label: "mmap + mlock", Set: map[string]any{"mmap": true, "mmlock": true, "direct_io": nil}},
		{Value: "direct_io", Label: "Direct I/O", Set: map[string]any{"mmap": nil, "mmlock": nil, "direct_io": true}},
	},
}

// allLayers is a short value that means every layer: llama.cpp clamps -ngl
// to the model's layer count, and no shipped model has 999 layers.
const allLayers = 999

var presets = []es.Preset{
	{
		ID: "bigMoe", Name: "Big MoE on a small GPU",
		Description: "All layers on the GPU, experts on the CPU, 8-bit KV cache.",
		Settings: map[string]any{
			"gpu_layers": allLayers, "flash_attention": "on", "cache_type_k": "q8_0", "cache_type_v": "q8_0",
			"parallel": 1, "no_op_offload": true,
		},
		Placement: &es.Placement{Mode: es.ExpertsCPUAll},
	},
	{
		ID: "maxContext", Name: "Max context",
		Description: "128K context, flash attention and an 8-bit KV cache.",
		Settings: map[string]any{
			"context_size": 131072, "flash_attention": "on", "cache_type_k": "q8_0", "cache_type_v": "q8_0", "parallel": 1,
		},
	},
	{
		ID: "lowVram", Name: "Low VRAM",
		Description: "4K context, a 4-bit KV cache and a small micro-batch.",
		Settings: map[string]any{
			"context_size": 4096, "flash_attention": "on", "cache_type_k": "q4_0", "cache_type_v": "q4_0",
			"ubatch": 128, "parallel": 1,
		},
	},
}

// Engine returns the llama.cpp engine.
func Engine() *es.Engine {
	return &es.Engine{
		ID:   ID,
		Name: "llama.cpp",
		// Every hardware build of the llama.cpp gRPC server ("cuda12-llama-cpp",
		// "metal-llama-cpp", ...) runs the same server; the empty backend is the
		// GGUF auto-detect path, which resolves to llama.cpp. ik-llama.cpp is a
		// separate engine that only shares the suffix.
		Backends: es.BackendMatch{
			Names:    []string{"llama", "go-llama"},
			Families: []string{"llama-cpp"},
			Excluded: []string{"ik-llama-cpp"},
			Empty:    true,
		},
		Groups:   []es.Group{GroupMemory, GroupSpeed, GroupPlacement, GroupSampling, GroupTemplate},
		Flags:    flags,
		Combined: []es.Combined{loadMode},
		Presets:  presets,
		// The MoE placement section of the editor writes these.
		ReservedOptions: []string{"cpu_moe", "n_cpu_moe", "moe_cache_mib"},
		Command: &es.CommandLine{
			Binary:  Binary,
			Example: "llama-server -m model.gguf --ctx-size 8192 -ngl 99 -fa on",
			IsBinary: func(word string) bool {
				base := path.Base(strings.ReplaceAll(word, `\`, "/"))
				return strings.Contains(base, "llama") || strings.Contains(base, "server")
			},
			Ignored: ignoredFlags,
			Special: []es.SpecialFlag{modelFlag, loadModeFlag},
		},
		Placement: &es.TensorPlacement{
			Option:    "-ot",
			Group:     GroupPlacement,
			Modes:     []es.PlacementMode{es.ExpertsCPUAll, es.ExpertsCPULayers, es.FirstLayersGPU},
			Build:     Option,
			Recognise: ParseOption,
		},
	}
}
