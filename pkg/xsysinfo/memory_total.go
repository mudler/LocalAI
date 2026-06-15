package xsysinfo

import (
	"strconv"
	"strings"
)

// cgroupV1UnlimitedSentinel is the value the kernel writes to
// memory.limit_in_bytes when no limit is set. It is PAGE_COUNTER_MAX
// (LONG_MAX rounded down to a page boundary), i.e. 0x7FFFFFFFFFFFF000 on
// 4 KiB-page systems. Any value at or above this is treated as "no limit".
const cgroupV1UnlimitedSentinel = uint64(0x7FFFFFFFFFFFF000)

// parseUintField parses a trimmed unsigned integer from raw file contents.
// It returns (0, false) when the content is empty or not a number.
func parseUintField(raw string) (uint64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseCgroupV2Max interprets the contents of cgroup v2 memory.max.
// The literal "max" means unlimited, returning 0.
func parseCgroupV2Max(raw string) uint64 {
	if strings.TrimSpace(raw) == "max" {
		return 0
	}
	v, ok := parseUintField(raw)
	if !ok {
		return 0
	}
	return v
}

// parseCgroupV1Limit interprets the contents of cgroup v1
// memory.limit_in_bytes. The kernel's "unlimited" sentinel (a value at or
// above PAGE_COUNTER_MAX) is treated as no limit, returning 0.
func parseCgroupV1Limit(raw string) uint64 {
	v, ok := parseUintField(raw)
	if !ok {
		return 0
	}
	if v >= cgroupV1UnlimitedSentinel {
		return 0
	}
	return v
}

// parseMemTotal extracts the MemTotal value (in bytes) from raw
// /proc/meminfo contents. MemTotal is reported in kibibytes, so the parsed
// value is multiplied by 1024. Returns 0 when the field is missing.
func parseMemTotal(raw string) uint64 {
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		// Expected: ["MemTotal:", "<value>", "kB"]
		if len(fields) < 2 {
			return 0
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		if len(fields) >= 3 {
			switch strings.ToLower(fields[2]) {
			case "kb":
				return v * 1024
			case "mb":
				return v * 1024 * 1024
			case "gb":
				return v * 1024 * 1024 * 1024
			}
		}
		return v
	}
	return 0
}

// chooseTotalMemory selects the most accurate system RAM total in bytes.
//
// On Linux the host kernel total (sysinfoTotal, from syscall.Sysinfo) is NOT
// virtualized by lxcfs/LXD, so inside a container it over-reports physical
// RAM. The cgroup limits and /proc/meminfo MemTotal, by contrast, do reflect
// the container's view. We therefore take the MINIMUM of all non-zero,
// non-unlimited candidates:
//
//   - cgroup v2 memory.max ("max" => unlimited, skipped)
//   - cgroup v1 memory.limit_in_bytes (kernel sentinel => unlimited, skipped)
//   - /proc/meminfo MemTotal (lxcfs/LXD virtualizes this)
//   - sysinfoTotal (bare-metal fallback)
//
// On bare metal the cgroup limits are unlimited and MemTotal == sysinfoTotal,
// so the result equals the host total exactly as before.
func chooseTotalMemory(cgroupV2Max, cgroupV1Limit, procMemInfo string, sysinfoTotal uint64) uint64 {
	candidates := []uint64{
		parseCgroupV2Max(cgroupV2Max),
		parseCgroupV1Limit(cgroupV1Limit),
		parseMemTotal(procMemInfo),
		sysinfoTotal,
	}

	var best uint64
	for _, c := range candidates {
		if c == 0 {
			continue
		}
		if best == 0 || c < best {
			best = c
		}
	}
	return best
}
