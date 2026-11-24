package xsysinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

const (
	clDeviceTypeGPU = "CL_DEVICE_TYPE_GPU"
	clinfoTimeout   = 2 * time.Second
)

// clinfoOutput is the subset of `clinfo --json` we read. clinfo emits
// one entry under "devices" per platform, in the same order as
// "platforms"; live devices are under "online".
type clinfoOutput struct {
	Devices []struct {
		Online []clinfoDevice `json:"online"`
	} `json:"devices"`
}

type clinfoDevice struct {
	Name              string         `json:"CL_DEVICE_NAME"`
	Vendor            string         `json:"CL_DEVICE_VENDOR"`
	VendorID          uint32         `json:"CL_DEVICE_VENDOR_ID"`
	Type              clinfoTypeProp `json:"CL_DEVICE_TYPE"`
	HostUnifiedMemory bool           `json:"CL_DEVICE_HOST_UNIFIED_MEMORY"`
	GlobalMemSize     uint64         `json:"CL_DEVICE_GLOBAL_MEM_SIZE"`
	PCIBusInfoKHR     string         `json:"CL_DEVICE_PCI_BUS_INFO_KHR"`
	PCIDomainNV       int            `json:"CL_DEVICE_PCI_DOMAIN_ID_NV"`
	PCIBusNV          int            `json:"CL_DEVICE_PCI_BUS_ID_NV"`
	PCISlotNV         int            `json:"CL_DEVICE_PCI_SLOT_ID_NV"`
}

// clinfoTypeProp matches against the type-string array rather than
// CL_DEVICE_TYPE.raw so a future CL_DEVICE_TYPE_CUSTOM can't sneak
// past as a GPU.
type clinfoTypeProp struct {
	Raw  uint32   `json:"raw"`
	Type []string `json:"type"`
}

func (t clinfoTypeProp) isGPU() bool {
	return slices.Contains(t.Type, clDeviceTypeGPU)
}

// clinfoOnce caches the result for the process lifetime. GPU hardware
// doesn't change between calls and the subprocess is ~150 ms.
var clinfoOnce = sync.OnceValue(runCLInfo)

func runCLInfo() []GPUMemoryInfo {
	if _, err := exec.LookPath("clinfo"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), clinfoTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "clinfo", "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		xlog.Debug("clinfo failed", "error", err, "stderr", stderr.String())
		return nil
	}
	return parseCLInfoJSON(stdout.Bytes())
}

// getCLInfoGPUMemory is a best-effort fallback for hosts where the
// vendor's own management binary (nvidia-smi / xpu-smi / rocm-smi)
// isn't installed but the OpenCL ICD is. Live used/free aren't exposed
// via standard CL_ properties; we synthesise them by attributing
// per-process VRAM allocations from the kernel DRM fdinfo interface
// to each clinfo-reported GPU via the shared PCI BDF.
func getCLInfoGPUMemory() []GPUMemoryInfo {
	gpus := clinfoOnce()
	if len(gpus) == 0 {
		return nil
	}
	usage := drmFdInfoUsageByBDF()
	for i := range gpus {
		gpus[i] = applyDRMUsage(gpus[i], usage[gpus[i].BDF])
	}
	return gpus
}

// applyDRMUsage stamps live VRAM accounting onto a GPUMemoryInfo
// whose TotalVRAM came from a static source (e.g. clinfo). Caller
// already populated TotalVRAM and FreeVRAM=TotalVRAM as defaults; if
// DRM accounting reports usage, we trust it and rederive free/percent.
func applyDRMUsage(g GPUMemoryInfo, used uint64) GPUMemoryInfo {
	if used == 0 || g.TotalVRAM == 0 {
		return g
	}
	if used > g.TotalVRAM {
		// Process-private DRM total can momentarily exceed device
		// VRAM (over-commit via host memory mirror). Clamp so the UI
		// doesn't display absurd percentages.
		used = g.TotalVRAM
	}
	g.UsedVRAM = used
	g.FreeVRAM = g.TotalVRAM - used
	g.UsagePercent = float64(used) / float64(g.TotalVRAM) * 100
	return g
}

// parseCLInfoJSON returns one GPUMemoryInfo per discrete GPU. UMA
// devices (iGPU/APU) are dropped because their "VRAM" is system RAM
// and would double-count against the capability gate. When the same
// physical device is enumerated by multiple ICDs (Intel OpenCL + POCL,
// for example), the BDF dedup keeps the largest reported size — some
// ICDs cap at 4 GiB for legacy alloc-size compatibility.
func parseCLInfoJSON(raw []byte) []GPUMemoryInfo {
	var out clinfoOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		xlog.Debug("clinfo: failed to parse --json output", "error", err)
		return nil
	}

	byBDF := map[string]GPUMemoryInfo{}
	var noBDF []GPUMemoryInfo

	for _, plat := range out.Devices {
		for _, d := range plat.Online {
			if !d.Type.isGPU() || d.HostUnifiedMemory || d.GlobalMemSize == 0 {
				continue
			}
			bdf := clinfoBDF(d)
			info := GPUMemoryInfo{
				Name:      strings.TrimSpace(d.Name),
				Vendor:    clinfoVendor(d.VendorID, d.Vendor),
				BDF:       bdf,
				TotalVRAM: d.GlobalMemSize,
				FreeVRAM:  d.GlobalMemSize,
			}
			if bdf == "" {
				noBDF = append(noBDF, info)
				continue
			}
			if existing, ok := byBDF[bdf]; !ok || info.TotalVRAM > existing.TotalVRAM {
				byBDF[bdf] = info
			}
		}
	}

	all := make([]GPUMemoryInfo, 0, len(byBDF)+len(noBDF))
	for _, g := range byBDF {
		all = append(all, g)
	}
	all = append(all, noBDF...)
	for i := range all {
		all[i].Index = i
	}
	return all
}

func clinfoVendor(vendorID uint32, name string) string {
	switch vendorID {
	case 0x10de:
		return VendorNVIDIA
	case 0x1002, 0x1022: // 0x1022 is the AMD CPU vendor ID, also reported by some APU OpenCL devices.
		return VendorAMD
	case 0x8086:
		return VendorIntel
	case 0x106B:
		return VendorApple
	}
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "nvidia"):
		return VendorNVIDIA
	case strings.Contains(n, "advanced micro devices"), strings.Contains(n, "amd"):
		return VendorAMD
	case strings.Contains(n, "intel"):
		return VendorIntel
	case strings.Contains(n, "apple"):
		return VendorApple
	}
	return VendorUnknown
}

// clinfoBDF returns the device's canonical `dddd:bb:dd.f` PCI address,
// or "" when no PCI location is reported. The KHR form is `"PCI-E,
// 0000:01:00.0"` on NVIDIA and bare `"0000:01:00.0"` on most others.
func clinfoBDF(d clinfoDevice) string {
	if d.PCIBusInfoKHR != "" {
		s := d.PCIBusInfoKHR
		if i := strings.LastIndex(s, " "); i >= 0 {
			s = s[i+1:]
		}
		if c := strings.Count(s, ":"); c == 1 || c == 2 {
			return normalizeBDF(s)
		}
	}
	// NVIDIA pre-KHR per-axis fields. An all-zero result is
	// indistinguishable from "fields absent", but no GPU sits at
	// 0000:00:00.0 so the false negative is harmless.
	if d.PCIBusNV != 0 || d.PCISlotNV != 0 || d.PCIDomainNV != 0 {
		return fmt.Sprintf("%04x:%02x:%02x.0", d.PCIDomainNV, d.PCIBusNV, d.PCISlotNV)
	}
	return ""
}

func normalizeBDF(s string) string {
	if strings.Count(s, ":") == 1 {
		return strings.ToLower("0000:" + s)
	}
	return strings.ToLower(s)
}
