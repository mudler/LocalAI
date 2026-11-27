package xsysinfo

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/xlog"
)

// vramTotalOverride is the total VRAM the operator says this host has
// (LOCALAI_VRAM_TOTAL). 0 = use detection. It replaces the detected total in
// every aggregate getter, so a worker reports it to the frontend and a
// standalone instance sizes models with it. The VRAM budget, when set, is
// then applied on top of it.
var vramTotalOverride atomic.Uint64

// SetVRAMTotalOverride sets the total VRAM of this host in bytes. 0 clears it.
func SetVRAMTotalOverride(bytes uint64) {
	vramTotalOverride.Store(bytes)
}

// VRAMTotalOverride returns the total VRAM set by the operator, or 0.
func VRAMTotalOverride() uint64 {
	return vramTotalOverride.Load()
}

// SetVRAMTotalFromString parses an absolute size ("40GB", "48GiB", bytes) and
// installs it as the VRAM total override. Empty clears the override. On a
// parse error the override is cleared and the error returned, so a typo falls
// back to detection instead of keeping an old value.
func SetVRAMTotalFromString(raw string) error {
	n, err := vrambudget.ParseSize(raw)
	if err != nil {
		SetVRAMTotalOverride(0)
		return err
	}
	SetVRAMTotalOverride(n)
	return nil
}

// ApplyVRAMTotalSetting installs the LOCALAI_VRAM_TOTAL setting at startup.
// A value that does not parse is logged and ignored, so a typo never stops
// the process: detection applies instead.
func ApplyVRAMTotalSetting(raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	if err := SetVRAMTotalFromString(raw); err != nil {
		xlog.Warn("Ignoring invalid LOCALAI_VRAM_TOTAL; using detected VRAM", "value", raw, "error", err)
		return
	}
	gpus, _ := GPUs()
	xlog.Info("VRAM total set by LOCALAI_VRAM_TOTAL; GPU detection is not used for the total",
		"vram_total", VRAMTotalOverride(), "detected", totalVRAM(GetGPUMemoryUsage(), gpus))
}

// maxPlausibleDeviceVRAM is the largest per-device VRAM figure accepted from
// a detection source. No GPU comes close: the largest discrete accelerators
// have a few hundred GB, and unified-memory hosts report system RAM. A value
// above this is a parse or driver error and is treated as unknown.
const maxPlausibleDeviceVRAM = uint64(2) << 40 // 2 TiB

// plausibleDevices returns infos with the memory of an implausible device
// set to unknown, logging a warning that names the device.
func plausibleDevices(infos []GPUMemoryInfo) []GPUMemoryInfo {
	out := infos
	copied := false
	for i, info := range infos {
		if info.TotalVRAM <= maxPlausibleDeviceVRAM {
			continue
		}
		if !copied {
			out = append([]GPUMemoryInfo(nil), infos...)
			copied = true
		}
		warnOnce("implausible-device:"+info.Name, "Ignoring an implausible VRAM total reported for a GPU; treating it as unknown. Set LOCALAI_VRAM_TOTAL if detection is wrong",
			"device", info.Name, "vendor", info.Vendor, "total_vram", info.TotalVRAM, "source", "vendor tool")
		out[i].TotalVRAM, out[i].UsedVRAM, out[i].FreeVRAM, out[i].UsagePercent = 0, 0, 0, 0
		out[i].UsageUnknown = true
	}
	return out
}

// ghwCardMemory returns the memory ghw attributes to each card it can use.
//
// ghw has no VRAM field. GraphicsCard.Node is the NUMA node the card is
// attached to, and Node.Memory is that node's system RAM. On a host with an
// integrated GPU that is the memory the GPU shares, so it is kept as a last
// resort. A discrete NVIDIA card never shares system RAM (the NVIDIA
// unified-memory SoCs are reported by getNVIDIAIntegratedGPUMemory before
// this fallback is reached), so for those cards the figure is dropped and
// logged: counting it is how a host with two 20 GB cards reported about
// 188 GiB of VRAM (#13531).
func ghwCardMemory(cards []*gpu.GraphicsCard) []uint64 {
	var out []uint64
	for _, card := range cards {
		if card == nil || card.Node == nil || card.Node.Memory == nil || card.Node.Memory.TotalUsableBytes <= 0 {
			continue
		}
		mem := uint64(card.Node.Memory.TotalUsableBytes)
		if vendorFromGHW([]*gpu.GraphicsCard{card}) == VendorNVIDIA {
			warnOnce("ghw-nvidia", "NVIDIA GPU found but nvidia-smi reported no memory for it. The memory ghw reports for the card is the system RAM of its NUMA node, not VRAM, so it is not counted. Check that the container has the NVIDIA utility capability (nvidia-smi), or set LOCALAI_VRAM_TOTAL",
				"source", "ghw", "numa_node_ram", mem)
			continue
		}
		out = append(out, mem)
	}
	return out
}

// minGHWCardMemory returns the smallest usable ghw figure, or 0.
func minGHWCardMemory(cards []*gpu.GraphicsCard) uint64 {
	var m uint64
	for _, v := range ghwCardMemory(cards) {
		if m == 0 || v < m {
			m = v
		}
	}
	return m
}

// perGPUOverride is the per-device VRAM when the operator set the total:
// the total split evenly over the GPUs, but never more than the smallest
// device the vendor tools report.
func perGPUOverride(total uint64, infos []GPUMemoryInfo, gpuCount int) uint64 {
	share := total
	if gpuCount > 1 {
		share = total / uint64(gpuCount)
	}
	if v := minNonZeroVRAM(infos); v > 0 && v < share {
		return v
	}
	return share
}

// gpuCountForOverride counts the GPUs the total override is split over: the
// devices the vendor tools list, else the ghw cards of the detected vendor.
func gpuCountForOverride(infos []GPUMemoryInfo, cards []*gpu.GraphicsCard) int {
	if len(infos) > 0 {
		return len(infos)
	}
	vendor := vendorFromGHW(cards)
	n := 0
	for _, card := range cards {
		if card != nil && vendor != "" && vendorFromGHW([]*gpu.GraphicsCard{card}) == vendor {
			n++
		}
	}
	return n
}

// applyVRAMTotalOverride replaces the summed total with the operator's
// figure. The used memory stays what the devices report; when no device
// reports its usage, the usage is unknown.
func applyVRAMTotalOverride(a GPUAggregateInfo, override uint64) GPUAggregateInfo {
	if override == 0 {
		return a
	}
	if a.TotalVRAM == 0 || a.UsageUnknown {
		a.TotalVRAM = override
		a.UsedVRAM, a.FreeVRAM, a.UsagePercent = 0, 0, 0
		a.UsageUnknown = true
		return a
	}
	a.TotalVRAM = override
	a.UsedVRAM = min(a.UsedVRAM, override)
	a.FreeVRAM = override - a.UsedVRAM
	a.UsagePercent = float64(a.UsedVRAM) / float64(override) * 100
	return a
}

var warned sync.Map

// warnOnce logs a detection warning once per process and key. The getters
// run on every heartbeat, and the same warning every few seconds is noise.
func warnOnce(key, msg string, fields ...any) {
	if _, loaded := warned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	xlog.Warn(msg, fields...)
}
