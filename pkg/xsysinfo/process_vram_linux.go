//go:build linux

// SPDX-License-Identifier: MIT
package xsysinfo

import (
	"bufio"
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcessVRAM reports device-local resident bytes accounted to a process tree
// by DRM. Unsupported or incomplete accounting returns false, not a measured zero.
func ProcessVRAM(pid int) (uint64, bool) {
	return processVRAM("/proc", pid)
}

func processVRAM(procRoot string, pid int) (uint64, bool) {
	if pid <= 0 {
		return 0, false
	}
	clients := map[string]uint64{}
	seen := map[int]bool{}
	pending := []int{pid}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		base := filepath.Join(procRoot, strconv.Itoa(current))
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if err != nil {
			return 0, false
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(base, "fd", fd.Name()))
			if err != nil {
				return 0, false
			}
			// A mixed DRM/NVIDIA tree cannot provide a complete DRM reading.
			if strings.HasPrefix(target, "/dev/nvidia") {
				return 0, false
			}
			if !strings.HasPrefix(target, "/dev/dri/render") {
				// Primary nodes can also own allocations. Until their device
				// identity is resolved, omitting them would undercount the tree.
				if strings.HasPrefix(target, "/dev/dri/") {
					return 0, false
				}
				continue
			}
			// #nosec G304 -- procRoot is /proc in production (a temp dir in tests);
			// base adds an integer PID, and fd.Name comes from os.ReadDir.
			// The kernel supplies these path components, not request input.
			data, err := os.ReadFile(filepath.Join(base, "fdinfo", fd.Name()))
			if err != nil {
				return 0, false
			}
			client, used, ok := drmResidentClient(data)
			if !ok {
				return 0, false
			}
			key := target + ":" + client
			// dup() and fork() can expose the same client more than once. The
			// snapshot is not atomic; retain its largest observed reading.
			clients[key] = max(clients[key], used)
		}

		// A worker may be spawned by any thread, not just the thread leader.
		tasks, err := os.ReadDir(filepath.Join(base, "task"))
		if err != nil || len(tasks) == 0 {
			return 0, false
		}
		for _, task := range tasks {
			// #nosec G304 -- procRoot is /proc in production (a temp dir in tests);
			// base adds an integer PID, and task.Name comes from os.ReadDir.
			// The kernel supplies these path components, not request input.
			data, err := os.ReadFile(filepath.Join(base, "task", task.Name(), "children"))
			if err != nil {
				return 0, false
			}
			for _, raw := range strings.Fields(string(data)) {
				child, err := strconv.Atoi(raw)
				if err != nil || child <= 0 {
					return 0, false
				}
				pending = append(pending, child)
			}
		}
	}
	var total uint64
	for _, used := range clients {
		if used > math.MaxUint64-total {
			return 0, false
		}
		total += used
	}
	return total, len(clients) > 0
}

func drmResidentClient(data []byte) (string, uint64, bool) {
	var client string
	var total uint64
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		if key == "drm-client-id" {
			id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return "", 0, false
			}
			client = strconv.FormatUint(id, 10)
		}
		region, resident := strings.CutPrefix(key, "drm-resident-")
		if !resident || !isVRAMRegion(region) {
			continue
		}
		used, ok := drmResidentBytes(value)
		if !ok || used > math.MaxUint64-total {
			return "", 0, false
		}
		total += used
		found = true
	}
	return client, total, scanner.Err() == nil && client != "" && found
}

func drmResidentBytes(value string) (uint64, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 || len(fields) > 2 {
		return 0, false
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	unit := uint64(1)
	if len(fields) == 2 {
		switch strings.ToLower(fields[1]) {
		case "b":
		case "kib":
			unit = 1 << 10
		case "mib":
			unit = 1 << 20
		case "gib":
			unit = 1 << 30
		default:
			return 0, false
		}
	}
	if n > math.MaxUint64/unit {
		return 0, false
	}
	return n * unit, true
}
