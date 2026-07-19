// Package system provides system detection utilities, including GPU/vendor detection
// and capability classification used to select optimal backends at runtime.
package system

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/mudler/xlog"
)

const (
	// Public constants - used by tests and external packages
	Nvidia = "nvidia"
	AMD    = "amd"
	Intel  = "intel"

	// Private constants - only used within this package
	defaultCapability = "default"
	disableCapability = "disable"
	nvidiaL4T         = "nvidia-l4t"
	darwinX86         = "darwin-x86"
	metal             = "metal"
	vulkan            = "vulkan"

	nvidiaCuda13    = "nvidia-cuda-13"
	nvidiaCuda12    = "nvidia-cuda-12"
	nvidiaL4TCuda12 = "nvidia-l4t-cuda-12"
	nvidiaL4TCuda13 = "nvidia-l4t-cuda-13"

	capabilityEnv        = "LOCALAI_FORCE_META_BACKEND_CAPABILITY"
	capabilityRunFileEnv = "LOCALAI_FORCE_META_BACKEND_CAPABILITY_RUN_FILE"
	defaultRunFile       = "/run/localai/capability"

	// Backend detection tokens (private)
	backendTokenDarwin = "darwin"
	backendTokenMLX    = "mlx"
	backendTokenMetal  = "metal"
	backendTokenL4T    = "l4t"
	backendTokenCUDA   = "cuda"
	backendTokenROCM   = "rocm"
	backendTokenHIP    = "hip"
	backendTokenSYCL   = "sycl"
	backendTokenCPU    = "cpu"

	// Engine names (private). Unlike the tokens above these are whole backend
	// identities as a gallery entry's `backend:` field spells them, not build
	// tags. See the two preference tables below for why the distinction matters.
	engineVLLM     = "vllm"
	engineSGLang   = "sglang"
	engineLlamaCpp = "llama-cpp"
	engineMLX      = "mlx"
)

// There are TWO preference tables below and they speak DIFFERENT VOCABULARIES.
// Merging them looks tempting and silently breaks one of the two consumers,
// because a token that means something in one vocabulary means nothing in the
// other. Read this before editing either.
//
//   - backendBuildTagPreferenceRules holds BUILD TAGS ("cuda", "rocm", "metal").
//     They are matched against INSTALLED BACKEND BUILD DIRECTORY NAMES such as
//     "llama-cpp-cuda-12" or "cuda12-vllm". Consumer: alias resolution in
//     ListSystemBackends (core/gallery/backends.go), which picks which installed
//     build of one alias to run.
//
//   - engineNamePreferenceRules holds ENGINE NAMES ("vllm", "llama-cpp", "mlx").
//     They are matched against a gallery entry's `backend:` value, which never
//     carries a build tag: no entry in gallery/index.yaml contains "cuda",
//     "rocm", "sycl" or "vulkan" anywhere in its backend name. Consumer: gallery
//     variant auto-selection (core/gallery/resolve_variant.go), which picks
//     which build of one model's weights to install.
//
// Feeding build tags to the variant ranker matches nothing, which does not
// error: every candidate simply scores equal and size alone decides, so the
// preference silently stops existing. That is exactly the bug this split fixes.

// backendPreferenceRule maps a detected capability to preferred tokens, best
// first. Both tables share this shape; only their vocabulary differs.
type backendPreferenceRule struct {
	capabilityPrefix string
	tokens           []string
}

// backendBuildTagPreferenceRules is the BUILD TAG table. See the block comment
// above for the vocabulary contract.
//
// Matching is by capability PREFIX, because a detected capability is refined at
// runtime ("nvidia" becomes "nvidia-cuda-12" when the toolkit is present) and a
// preference is about the vendor rather than the point release. Rules are tried
// in order, so a more specific prefix must precede any rule it shares a prefix
// with.
//
// Tokens are matched as substrings of a build directory name, so "cuda" covers
// "cuda12-llama-cpp". A build matching no token is not an error and is never
// discarded; it simply sorts below every recognised one.
var backendBuildTagPreferenceRules = []backendPreferenceRule{
	{Nvidia, []string{backendTokenCUDA, vulkan, backendTokenCPU}},
	{AMD, []string{backendTokenROCM, backendTokenHIP, vulkan, backendTokenCPU}},
	{Intel, []string{backendTokenSYCL, Intel, backendTokenCPU}},
	{metal, []string{backendTokenMetal, backendTokenCPU}},
	{darwinX86, []string{darwinX86, backendTokenCPU}},
	{vulkan, []string{vulkan, backendTokenCPU}},
}

// defaultBackendBuildTagTokens is what a host with no matching rule prefers.
// A capability nobody has taught this table about degrades to plain CPU rather
// than to an error or to an empty list.
var defaultBackendBuildTagTokens = []string{backendTokenCPU}

// engineNamePreferenceRules is the ENGINE NAME table. See the block comment
// above for the vocabulary contract.
//
// Capability matching is by prefix, exactly as the build tag table does it.
//
// Tokens are matched as SUBSTRINGS of an engine name, which is load bearing:
// "vllm" also covers "vllm-omni", "mlx" also covers "mlx-vlm" and "mlx-audio",
// and "llama-cpp" also covers "ik-llama-cpp". Each of those is a build of the
// engine named by the token, so ranking them together is correct. Order the
// tokens so no token is a substring of an engine that should rank differently.
//
// A capability is deliberately ABSENT rather than guessed at when no engine
// ordering can be justified for it. An absent rule degrades to ordering by size
// alone, which is the behaviour that predates preference and is always safe.
//
// Safe, however, only where every candidate engine is equally at home on the
// host. Where one is not, the rule has to be written here, because the hardware
// filter will not write it: IsBackendCompatible derives support from the engine
// NAME, and "vllm" and "sglang" contain none of the darwin, cuda, rocm or sycl
// tokens it keys on, so a GPU serving engine is never filtered out on a host
// with no GPU. Left unranked there, it wins on size alone whenever its build is
// the larger one, and a CPU-only box installs vLLM over llama.cpp.
var engineNamePreferenceRules = []backendPreferenceRule{
	// vLLM first on every host with a dedicated serving engine build. It is the
	// throughput engine, and a model published with a vLLM build is published
	// that way precisely because that build is the one worth running.
	// SGLang sits directly behind it: same class of GPU serving engine, ships
	// cuda/rocm/intel builds alike, but it is behind vLLM because vLLM covers
	// far more of the gallery. llama-cpp is the portable fallback.
	{Nvidia, []string{engineVLLM, engineSGLang, engineLlamaCpp}},
	{AMD, []string{engineVLLM, engineSGLang, engineLlamaCpp}},
	{Intel, []string{engineVLLM, engineSGLang, engineLlamaCpp}},
	// MLX is the native accelerated runtime on Apple silicon, whereas a
	// metal-enabled GGUF build is the portable engine merely compiled with GPU
	// offload. No vLLM or SGLang build targets metal, so neither is listed.
	{metal, []string{engineMLX, engineLlamaCpp}},
	// A Vulkan host has exactly one LLM engine with a Vulkan build, so a
	// llama-cpp variant is the only one that will use the GPU at all.
	{vulkan, []string{engineLlamaCpp}},
	// No usable accelerator: either nothing was detected, or a GPU was found
	// with too little VRAM to serve from, both of which report "default".
	// llama.cpp is the engine built to run well on a CPU; vLLM and SGLang are
	// GPU serving engines whose CPU paths exist but are not what an unattended
	// auto-selection should hand a CPU-only box.
	//
	// The GPU engines are enumerated behind llama.cpp rather than left
	// unmatched. Listing llama.cpp alone would already make it win, since an
	// unmatched engine ranks below every listed one, but it would leave vLLM
	// and SGLang tied with each other and with any engine nobody has ranked
	// yet, so download size would decide among them. Naming them fixes that
	// order and says the omission of a GPU engine from the top spot is a
	// decision rather than an oversight. Ranking never filters, so a vLLM
	// variant offered alone is still installed here.
	{defaultCapability, []string{engineLlamaCpp, engineVLLM, engineSGLang}},
	// An Intel Mac has no accelerated runtime at all: MLX needs Apple silicon,
	// and no vLLM or SGLang build targets darwin. Same ordering, same reasons.
	// MLX is deliberately left unlisted so it ranks last, since
	// IsBackendCompatible admits any darwin-tokened engine on this capability
	// and will not drop it.
	{darwinX86, []string{engineLlamaCpp, engineVLLM, engineSGLang}},
}

// defaultEnginePreferenceTokens is empty on purpose. It is now only reached by
// a capability nobody has taught this table about, which an operator can force
// via LOCALAI_FORCE_META_BACKEND_CAPABILITY; the detected "default" has its own
// rule above. Guessing an order for hardware we know nothing about would be
// worse than ordering by size alone, which is what the ranker reads an empty
// list as and is the behaviour that predates preference.
var defaultEnginePreferenceTokens = []string{}

var (
	cuda13DirExists bool
	cuda12DirExists bool
)

func init() {
	_, err := os.Stat(filepath.Join(string(os.PathSeparator), "usr", "local", "cuda-13"))
	cuda13DirExists = err == nil
	_, err = os.Stat(filepath.Join(string(os.PathSeparator), "usr", "local", "cuda-12"))
	cuda12DirExists = err == nil
}

// CapabilityFilterDisabled returns true when capability-based backend filtering
// is disabled via LOCALAI_FORCE_META_BACKEND_CAPABILITY=disable.
func (s *SystemState) CapabilityFilterDisabled() bool {
	return s.getSystemCapabilities() == disableCapability
}

func (s *SystemState) Capability(capMap map[string]string) string {
	reportedCapability := s.getSystemCapabilities()

	// Check if the reported capability is in the map
	if _, exists := capMap[reportedCapability]; exists {
		xlog.Debug("Using reported capability", "reportedCapability", reportedCapability, "capMap", capMap)
		return reportedCapability
	}

	// Fall back to the explicit "default" catch-all, then to "cpu". The cpu
	// fallback matters for meta backends that only enumerate GPU variants +
	// cpu (e.g. vllm maps nvidia/amd/intel/cpu but not default): on a
	// no-GPU host the reported capability is "default", so without this
	// we'd filter the meta out and break auto-install by name.
	if _, exists := capMap[defaultCapability]; exists {
		xlog.Debug("Capability not in map, falling back to default", "reportedCapability", reportedCapability, "capMap", capMap)
		return defaultCapability
	}
	if _, exists := capMap["cpu"]; exists {
		xlog.Debug("Capability not in map, falling back to cpu", "reportedCapability", reportedCapability, "capMap", capMap)
		return "cpu"
	}

	xlog.Debug("The requested capability was not found, using default capability", "reportedCapability", reportedCapability, "capMap", capMap)
	return defaultCapability
}

func (s *SystemState) getSystemCapabilities() string {

	if s.systemCapabilities != "" {
		return s.systemCapabilities
	}

	capability := os.Getenv(capabilityEnv)
	if capability != "" {
		xlog.Info("Using forced capability from environment variable", "capability", capability, "env", capabilityEnv)
		s.systemCapabilities = capability
		return capability
	}

	capabilityRunFile := defaultRunFile
	capabilityRunFileEnv := os.Getenv(capabilityRunFileEnv)
	if capabilityRunFileEnv != "" {
		capabilityRunFile = capabilityRunFileEnv
	}

	// Check if /run/localai/capability exists and use it
	// This might be used by e.g. container images to specify which
	// backends to pull in automatically when installing meta backends.
	if _, err := os.Stat(capabilityRunFile); err == nil {
		capability, err := os.ReadFile(capabilityRunFile)
		if err == nil {
			xlog.Info("Using forced capability run file", "capabilityRunFile", capabilityRunFile, "capability", string(capability), "env", capabilityRunFileEnv)
			s.systemCapabilities = strings.Trim(strings.TrimSpace(string(capability)), "\n")
			return s.systemCapabilities
		}
	}

	// If we are on mac and arm64, we will return metal
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		xlog.Info("Using metal capability (arm64 on mac)", "env", capabilityEnv)
		s.systemCapabilities = metal
		return s.systemCapabilities
	}

	// If we are on mac and x86, we will return darwin-x86
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		xlog.Info("Using darwin-x86 capability (amd64 on mac)", "env", capabilityEnv)
		s.systemCapabilities = darwinX86
		return s.systemCapabilities
	}

	// If arm64 on linux and a nvidia gpu is detected, we will return nvidia-l4t
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if s.GPUVendor == Nvidia {
			xlog.Info("Using nvidia-l4t capability (arm64 on linux)", "env", capabilityEnv)
			if cuda13DirExists {
				s.systemCapabilities = nvidiaL4TCuda13
				return s.systemCapabilities
			}
			if cuda12DirExists {
				s.systemCapabilities = nvidiaL4TCuda12
				return s.systemCapabilities
			}
			s.systemCapabilities = nvidiaL4T
			return s.systemCapabilities
		}
	}

	// No GPU detected → default capability
	if s.GPUVendor == "" {
		xlog.Info("Default capability (no GPU detected)", "env", capabilityEnv)
		s.systemCapabilities = defaultCapability
		return s.systemCapabilities
	}

	// GPU detected but insufficient VRAM → default with warning
	if s.VRAM <= 4*1024*1024*1024 {
		xlog.Warn("VRAM is less than 4GB, defaulting to CPU", "env", capabilityEnv)
		s.systemCapabilities = defaultCapability
		return s.systemCapabilities
	}

	// CUDA directories refine capability only for NVIDIA GPUs
	if s.GPUVendor == Nvidia {
		if cuda13DirExists {
			s.systemCapabilities = nvidiaCuda13
			return s.systemCapabilities
		}
		if cuda12DirExists {
			s.systemCapabilities = nvidiaCuda12
			return s.systemCapabilities
		}
	}

	s.systemCapabilities = s.GPUVendor
	return s.systemCapabilities
}

// BackendPreferenceTokens returns a list of substrings that represent the preferred
// backend implementation order for the current system capability. Callers can use
// these tokens to select the most appropriate concrete backend among multiple
// candidates sharing the same alias (e.g., "llama-cpp").
//
// These are BUILD TAGS matched against installed build directory names. For
// engine names as a gallery entry spells them, use EnginePreferenceTokens.
func (s *SystemState) BackendPreferenceTokens() []string {
	return s.preferenceTokens(backendBuildTagPreferenceRules, defaultBackendBuildTagTokens)
}

// EnginePreferenceTokens returns the engine names this host prefers, best first,
// for ranking gallery model variants against each other.
//
// These are ENGINE NAMES matched against a gallery entry's `backend:` value
// ("vllm", "llama-cpp", "mlx"), never build tags. Feeding this function's output
// to installed-build alias resolution, or BackendPreferenceTokens' output to
// variant ranking, matches nothing and silently disables the preference.
//
// An empty result is normal and means "no engine ordering applies here", which
// the ranker reads as ordering by size alone.
func (s *SystemState) EnginePreferenceTokens() []string {
	return s.preferenceTokens(engineNamePreferenceRules, defaultEnginePreferenceTokens)
}

// preferenceTokens resolves the current capability against one preference table.
// The rules live in the tables above; this only looks them up, so teaching
// LocalAI about a new runtime never means editing logic.
func (s *SystemState) preferenceTokens(rules []backendPreferenceRule, fallback []string) []string {
	capStr := strings.ToLower(s.getSystemCapabilities())
	for _, rule := range rules {
		if strings.HasPrefix(capStr, rule.capabilityPrefix) {
			// Copied so a caller cannot mutate the shared table out from under
			// every other host lookup.
			return slices.Clone(rule.tokens)
		}
	}
	return slices.Clone(fallback)
}

// DetectedCapability returns the raw detected capability string (e.g. "metal",
// "nvidia-cuda-12", "default") with no map-membership fallback applied.
// This can be used by the UI to display what capability was detected.
//
// Why this exists alongside Capability: Capability resolves against a caller
// supplied map and falls back to "default" then "cpu" when the detected value
// is absent from that map, so its answer describes what that caller can serve
// rather than what the hardware is. A caller reasoning about the hardware
// itself, or reporting it to a human, cannot tell a genuinely detected
// "default" apart from a substituted one and needs the undecorated value.
func (s *SystemState) DetectedCapability() string {
	return s.getSystemCapabilities()
}

// IsBackendCompatible checks if a backend (identified by name and URI) is compatible
// with the current system capability. This function uses getSystemCapabilities to ensure
// consistency with capability detection (including VRAM checks, environment overrides, etc.).
func (s *SystemState) IsBackendCompatible(name, uri string) bool {
	if s.CapabilityFilterDisabled() {
		return true
	}

	combined := strings.ToLower(name + " " + uri)
	capability := s.getSystemCapabilities()

	// Check for darwin/macOS-specific backends (mlx, metal, darwin)
	isDarwinBackend := strings.Contains(combined, backendTokenDarwin) ||
		strings.Contains(combined, backendTokenMLX) ||
		strings.Contains(combined, backendTokenMetal)
	if isDarwinBackend {
		// Darwin backends require the system to be running on darwin with metal or darwin-x86 capability
		return capability == metal || capability == darwinX86
	}

	// Check for NVIDIA L4T-specific backends (arm64 Linux with NVIDIA GPU)
	// This must be checked before the general NVIDIA check as L4T backends
	// may also contain "cuda" or "nvidia" in their names
	isL4TBackend := strings.Contains(combined, backendTokenL4T)
	if isL4TBackend {
		return strings.HasPrefix(capability, nvidiaL4T)
	}

	// Check for NVIDIA/CUDA-specific backends (non-L4T)
	isNvidiaBackend := strings.Contains(combined, backendTokenCUDA) ||
		strings.Contains(combined, Nvidia)
	if isNvidiaBackend {
		// NVIDIA backends are compatible with nvidia, nvidia-cuda-12, nvidia-cuda-13, and l4t capabilities
		return strings.HasPrefix(capability, Nvidia)
	}

	// Check for AMD/ROCm-specific backends
	isAMDBackend := strings.Contains(combined, backendTokenROCM) ||
		strings.Contains(combined, backendTokenHIP) ||
		strings.Contains(combined, AMD)
	if isAMDBackend {
		return capability == AMD
	}

	// Check for Intel/SYCL-specific backends
	isIntelBackend := strings.Contains(combined, backendTokenSYCL) ||
		strings.Contains(combined, Intel)
	if isIntelBackend {
		return capability == Intel
	}

	// CPU backends are always compatible
	return true
}
