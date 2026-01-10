// Package system provides system detection utilities, including GPU/vendor detection
// and capability classification used to select optimal backends at runtime.
package system

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mudler/xlog"
)

const (
	DefaultCapability = "default"
	NvidiaL4T         = "nvidia-l4t"
	DarwinX86         = "darwin-x86"
	Metal             = "metal"
	Nvidia            = "nvidia"

	AMD    = "amd"
	Intel  = "intel"
	Vulkan = "vulkan"

	NvidiaCuda13    = "nvidia-cuda-13"
	NvidiaCuda12    = "nvidia-cuda-12"
	NvidiaL4TCuda12 = "nvidia-l4t-cuda-12"
	NvidiaL4TCuda13 = "nvidia-l4t-cuda-13"

	capabilityEnv        = "LOCALAI_FORCE_META_BACKEND_CAPABILITY"
	capabilityRunFileEnv = "LOCALAI_FORCE_META_BACKEND_CAPABILITY_RUN_FILE"
	defaultRunFile       = "/run/localai/capability"

	// Backend detection tokens
	BackendTokenDarwin = "darwin"
	BackendTokenMLX    = "mlx"
	BackendTokenMetal  = "metal"
	BackendTokenL4T    = "l4t"
	BackendTokenCUDA   = "cuda"
	BackendTokenROCM   = "rocm"
	BackendTokenHIP    = "hip"
	BackendTokenSYCL   = "sycl"
)

var (
	cuda13DirExists bool
	cuda12DirExists bool
)

func init() {
	_, err := os.Stat(filepath.Join("usr", "local", "cuda-13"))
	cuda13DirExists = err == nil
	_, err = os.Stat(filepath.Join("usr", "local", "cuda-12"))
	cuda12DirExists = err == nil
}

func (s *SystemState) Capability(capMap map[string]string) string {
	reportedCapability := s.getSystemCapabilities()

	// Check if the reported capability is in the map
	if _, exists := capMap[reportedCapability]; exists {
		xlog.Debug("Using reported capability", "reportedCapability", reportedCapability, "capMap", capMap)
		return reportedCapability
	}

	xlog.Debug("The requested capability was not found, using default capability", "reportedCapability", reportedCapability, "capMap", capMap)
	// Otherwise, return the default capability (catch-all)
	return DefaultCapability
}

func (s *SystemState) getSystemCapabilities() string {
	capability := os.Getenv(capabilityEnv)
	if capability != "" {
		xlog.Info("Using forced capability from environment variable", "capability", capability, "env", capabilityEnv)
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
			return strings.Trim(strings.TrimSpace(string(capability)), "\n")
		}
	}

	// If we are on mac and arm64, we will return metal
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		xlog.Info("Using metal capability (arm64 on mac)", "env", capabilityEnv)
		return Metal
	}

	// If we are on mac and x86, we will return darwin-x86
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		xlog.Info("Using darwin-x86 capability (amd64 on mac)", "env", capabilityEnv)
		return DarwinX86
	}

	// If arm64 on linux and a nvidia gpu is detected, we will return nvidia-l4t
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if s.GPUVendor == Nvidia {
			xlog.Info("Using nvidia-l4t capability (arm64 on linux)", "env", capabilityEnv)
			if cuda13DirExists {
				return NvidiaL4TCuda13
			}
			if cuda12DirExists {
				return NvidiaL4TCuda12
			}
			return NvidiaL4T
		}
	}

	if cuda13DirExists {
		return NvidiaCuda13
	}

	if cuda12DirExists {
		return NvidiaCuda12
	}

	if s.GPUVendor == "" {
		xlog.Info("Default capability (no GPU detected)", "env", capabilityEnv)
		return DefaultCapability
	}

	xlog.Info("Capability automatically detected", "capability", s.GPUVendor, "env", capabilityEnv)
	// If vram is less than 4GB, let's default to CPU but warn the user that they can override that via env
	if s.VRAM <= 4*1024*1024*1024 {
		xlog.Warn("VRAM is less than 4GB, defaulting to CPU", "env", capabilityEnv)
		return DefaultCapability
	}

	return s.GPUVendor
}

// BackendPreferenceTokens returns a list of substrings that represent the preferred
// backend implementation order for the current system capability. Callers can use
// these tokens to select the most appropriate concrete backend among multiple
// candidates sharing the same alias (e.g., "llama-cpp").
func (s *SystemState) BackendPreferenceTokens() []string {
	capStr := strings.ToLower(s.getSystemCapabilities())
	switch {
	case strings.HasPrefix(capStr, Nvidia):
		return []string{BackendTokenCUDA, Vulkan, "cpu"}
	case strings.HasPrefix(capStr, AMD):
		return []string{BackendTokenROCM, BackendTokenHIP, Vulkan, "cpu"}
	case strings.HasPrefix(capStr, Intel):
		return []string{BackendTokenSYCL, Intel, "cpu"}
	case strings.HasPrefix(capStr, Metal):
		return []string{BackendTokenMetal, "cpu"}
	case strings.HasPrefix(capStr, DarwinX86):
		return []string{"darwin-x86", "cpu"}
	case strings.HasPrefix(capStr, Vulkan):
		return []string{Vulkan, "cpu"}
	default:
		return []string{"cpu"}
	}
}

// DetectedCapability returns the detected system capability string.
// This can be used by the UI to display what capability was detected.
func (s *SystemState) DetectedCapability() string {
	return s.getSystemCapabilities()
}

// IsBackendCompatible checks if a backend (identified by name and URI) is compatible
// with the current system capability. This function uses getSystemCapabilities to ensure
// consistency with capability detection (including VRAM checks, environment overrides, etc.).
func (s *SystemState) IsBackendCompatible(name, uri string) bool {
	combined := strings.ToLower(name + " " + uri)
	capability := s.getSystemCapabilities()

	// Check for darwin/macOS-specific backends (mlx, metal, darwin)
	isDarwinBackend := strings.Contains(combined, BackendTokenDarwin) ||
		strings.Contains(combined, BackendTokenMLX) ||
		strings.Contains(combined, BackendTokenMetal)
	if isDarwinBackend {
		// Darwin backends require the system to be running on darwin with metal or darwin-x86 capability
		return capability == Metal || capability == DarwinX86
	}

	// Check for NVIDIA L4T-specific backends (arm64 Linux with NVIDIA GPU)
	// This must be checked before the general NVIDIA check as L4T backends
	// may also contain "cuda" or "nvidia" in their names
	isL4TBackend := strings.Contains(combined, BackendTokenL4T)
	if isL4TBackend {
		return strings.HasPrefix(capability, NvidiaL4T)
	}

	// Check for NVIDIA/CUDA-specific backends (non-L4T)
	isNvidiaBackend := strings.Contains(combined, BackendTokenCUDA) ||
		strings.Contains(combined, Nvidia)
	if isNvidiaBackend {
		// NVIDIA backends are compatible with nvidia, nvidia-cuda-12, nvidia-cuda-13, and l4t capabilities
		return strings.HasPrefix(capability, Nvidia)
	}

	// Check for AMD/ROCm-specific backends
	isAMDBackend := strings.Contains(combined, BackendTokenROCM) ||
		strings.Contains(combined, BackendTokenHIP) ||
		strings.Contains(combined, AMD)
	if isAMDBackend {
		return capability == AMD
	}

	// Check for Intel/SYCL-specific backends
	isIntelBackend := strings.Contains(combined, BackendTokenSYCL) ||
		strings.Contains(combined, Intel)
	if isIntelBackend {
		return capability == Intel
	}

	// CPU backends are always compatible
	return true
}
