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
	// Public constants - used by tests and external packages
	Nvidia = "nvidia"
	AMD    = "amd"
	Intel  = "intel"

	// Private constants - only used within this package
	defaultCapability = "default"
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
)

var (
	cuda13DirExists  bool
	cuda12DirExists  bool
	capabilityLogged bool

	forceCapabilityRunFile    string
	forceCapabilityRunFileSet bool
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
	return defaultCapability
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

	if !forceCapabilityRunFileSet || forceCapabilityRunFile != capabilityRunFile {
		forceCapabilityRunFile = capabilityRunFile
		forceCapabilityRunFileSet = true

		if _, err := os.Stat(capabilityRunFile); err == nil {
			capability, err := os.ReadFile(capabilityRunFile)
			if err == nil {
				xlog.Info("Using forced capability run file", "capabilityRunFile", capabilityRunFile, "capability", string(capability), "env", capabilityRunFileEnv)
				return strings.Trim(strings.TrimSpace(string(capability)), "\n")
			}
		}
	}

	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		xlog.Info("Using metal capability (arm64 on mac)", "env", capabilityEnv)
		return metal
	}

	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		xlog.Info("Using darwin-x86 capability (amd64 on mac)", "env", capabilityEnv)
		return darwinX86
	}

	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if s.GPUVendor == Nvidia {
			xlog.Info("Using nvidia-l4t capability (arm64 on linux)", "env", capabilityEnv)
			if cuda13DirExists {
				return nvidiaL4TCuda13
			}
			if cuda12DirExists {
				return nvidiaL4TCuda12
			}
			return nvidiaL4T
		}
	}

	if cuda13DirExists {
		return nvidiaCuda13
	}

	if cuda12DirExists {
		return nvidiaCuda12
	}

	if s.GPUVendor == "" {
		xlog.Info("Default capability (no GPU detected)", "env", capabilityEnv)
		return defaultCapability
	}

	if !capabilityLogged {
		xlog.Info("Capability automatically detected", "capability", s.GPUVendor, "env", capabilityEnv)
		capabilityLogged = true
	}

	if s.VRAM <= 4*1024*1024*1024 {
		xlog.Warn("VRAM is less than 4GB, defaulting to CPU", "env", capabilityEnv)
		return defaultCapability
	}

	return s.GPUVendor
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
		return metal
	}

	// If we are on mac and x86, we will return darwin-x86
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		xlog.Info("Using darwin-x86 capability (amd64 on mac)", "env", capabilityEnv)
		return darwinX86
	}

	// If arm64 on linux and a nvidia gpu is detected, we will return nvidia-l4t
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if s.GPUVendor == Nvidia {
			xlog.Info("Using nvidia-l4t capability (arm64 on linux)", "env", capabilityEnv)
			if cuda13DirExists {
				return nvidiaL4TCuda13
			}
			if cuda12DirExists {
				return nvidiaL4TCuda12
			}
			return nvidiaL4T
		}
	}

	if cuda13DirExists {
		return nvidiaCuda13
	}

	if cuda12DirExists {
		return nvidiaCuda12
	}

	if s.GPUVendor == "" {
		xlog.Info("Default capability (no GPU detected)", "env", capabilityEnv)
		return defaultCapability
	}

	if !capabilityLogged {
		xlog.Info("Capability automatically detected", "capability", s.GPUVendor, "env", capabilityEnv)
		capabilityLogged = true
	}
	// If vram is less than 4GB, let's default to CPU but warn the user that they can override that via env
	if s.VRAM <= 4*1024*1024*1024 {
		xlog.Warn("VRAM is less than 4GB, defaulting to CPU", "env", capabilityEnv)
		return defaultCapability
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
		return []string{backendTokenCUDA, vulkan, "cpu"}
	case strings.HasPrefix(capStr, AMD):
		return []string{backendTokenROCM, backendTokenHIP, vulkan, "cpu"}
	case strings.HasPrefix(capStr, Intel):
		return []string{backendTokenSYCL, Intel, "cpu"}
	case strings.HasPrefix(capStr, metal):
		return []string{backendTokenMetal, "cpu"}
	case strings.HasPrefix(capStr, darwinX86):
		return []string{"darwin-x86", "cpu"}
	case strings.HasPrefix(capStr, vulkan):
		return []string{vulkan, "cpu"}
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
