package xsysinfo

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// drmFdInfoUsageByBDF walks /proc/<pid>/fdinfo/<fd> for every fd that
// points at /dev/dri/render* and aggregates per-GPU VRAM allocations.
// Keyed by the PCI BDF (dddd:bb:dd.f) of the render node so callers
// can match against any GPU detection result.
//
// The kernel exposes per-process DRM accounting via standardised
// fdinfo keys (Documentation/gpu/drm-usage-stats.rst, kernel ≥5.19):
//
//	drm-total-<region>:    bytes the process has bound to <region>
//	drm-resident-<region>: bytes currently resident in <region>
//
// Region names are driver-defined: i915 uses "local*" for device-local
// VRAM, amdgpu and xe use "vram*". We sum any region whose name
// starts with "local" or "vram"; "system*" / "gtt*" / "stolen-*" are
// excluded since they're host RAM mirrors.
//
// Returns an empty map when no process holds a DRM render fd or the
// kernel doesn't emit the accounting keys (older kernels, exotic
// drivers). The walker is read-only and survives unreadable proc
// entries (other users' processes, transient PIDs).
func drmFdInfoUsageByBDF() map[string]uint64 {
	byRender := drmFdInfoUsageByRenderNode()
	if len(byRender) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(byRender))
	for name, used := range byRender {
		bdf := renderNodeBDF(name)
		if bdf == "" {
			continue
		}
		out[bdf] += used
	}
	return out
}

func drmFdInfoUsageByRenderNode() map[string]uint64 {
	procs, _ := filepath.Glob("/proc/[0-9]*/fd")
	if len(procs) == 0 {
		return nil
	}
	out := map[string]uint64{}
	for _, fdDir := range procs {
		pidDir := filepath.Dir(fdDir)
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			// /proc race: process exited or unreadable. Skip silently.
			continue
		}
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
			if err != nil {
				continue
			}
			const renderPrefix = "/dev/dri/render"
			if !strings.HasPrefix(target, renderPrefix) {
				continue
			}
			renderName := strings.TrimPrefix(target, "/dev/dri/")
			data, err := os.ReadFile(filepath.Join(pidDir, "fdinfo", entry.Name()))
			if err != nil {
				continue
			}
			out[renderName] += parseDRMFdInfoVRAM(data)
		}
	}
	return out
}

// parseDRMFdInfoVRAM sums `drm-total-<region>` bytes across all VRAM
// regions in a single fdinfo blob. Values are formatted as
// "<number> <KiB|MiB|GiB>" or bare bytes; both are accepted.
func parseDRMFdInfoVRAM(data []byte) uint64 {
	var total uint64
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		const prefix = "drm-total-"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		region := strings.TrimPrefix(key, prefix)
		if !isVRAMRegion(region) {
			continue
		}
		total += parseDRMFdInfoBytes(value)
	}
	return total
}

func isVRAMRegion(region string) bool {
	return strings.HasPrefix(region, "local") || strings.HasPrefix(region, "vram")
}

func parseDRMFdInfoBytes(value string) uint64 {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	if len(fields) < 2 {
		return n
	}
	switch strings.ToLower(fields[1]) {
	case "kib":
		return n * 1024
	case "mib":
		return n * 1024 * 1024
	case "gib":
		return n * 1024 * 1024 * 1024
	}
	return n
}

// renderNodeBDF resolves a DRM render-node basename (e.g. "renderD129")
// to its underlying PCI BDF by following /sys/class/drm/<name>/device.
// Returns "" for non-PCI devices or symlink read errors.
func renderNodeBDF(name string) string {
	link, err := os.Readlink("/sys/class/drm/" + name + "/device")
	if err != nil {
		return ""
	}
	base := filepath.Base(link)
	// Sanity-check: BDF format is dddd:bb:dd.f
	if strings.Count(base, ":") != 2 || strings.Count(base, ".") != 1 {
		return ""
	}
	return strings.ToLower(base)
}
