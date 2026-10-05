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
	// Filled once, when a kernel without CONFIG_PROC_CHILDREN is detected.
	var childIndex map[int][]int
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
		children, ok := directChildPIDs(procRoot, base, current, &childIndex)
		if !ok {
			return 0, false
		}
		pending = append(pending, children...)
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

// directChildPIDs lists processes forked by pid. Kernels without
// CONFIG_PROC_CHILDREN have no task/<tid>/children file. That absence is not
// an incomplete tree, so the walk continues from /proc/<pid>/stat ppid links.
// A task that disappears mid-read, or any other error, still fails the reading.
func directChildPIDs(procRoot, base string, pid int, childIndex *map[int][]int) ([]int, bool) {
	tasks, err := os.ReadDir(filepath.Join(base, "task"))
	if err != nil || len(tasks) == 0 {
		return nil, false
	}
	if *childIndex != nil {
		return (*childIndex)[pid], true
	}
	var children []int
	for _, task := range tasks {
		taskDir := filepath.Join(base, "task", task.Name())
		// #nosec G304 -- procRoot is /proc in production (a temp dir in tests);
		// base adds an integer PID, and task.Name comes from os.ReadDir.
		// The kernel supplies these path components, not request input.
		data, err := os.ReadFile(filepath.Join(taskDir, "children"))
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, false
			}
			if _, statErr := os.Stat(taskDir); statErr != nil {
				return nil, false
			}
			mapped, ok := childPIDsByPPID(procRoot)
			if !ok {
				return nil, false
			}
			*childIndex = mapped
			return mapped[pid], true
		}
		for _, raw := range strings.Fields(string(data)) {
			child, err := strconv.Atoi(raw)
			if err != nil || child <= 0 {
				return nil, false
			}
			children = append(children, child)
		}
	}
	return children, true
}

func childPIDsByPPID(procRoot string) (map[int][]int, bool) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, false
	}
	children := map[int][]int{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		// #nosec G304 -- procRoot is /proc in production (a temp dir in tests);
		// entry.Name is a numeric directory from os.ReadDir.
		data, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "stat"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, false
		}
		ppid, ok := ppidFromStat(string(data))
		if !ok {
			return nil, false
		}
		children[ppid] = append(children[ppid], pid)
	}
	return children, true
}

// ppidFromStat reads field 4 of /proc/<pid>/stat. The comm field is wrapped in
// parentheses and may contain spaces or ')'.
func ppidFromStat(data string) (int, bool) {
	end := strings.LastIndex(data, ")")
	if end < 0 || end+1 >= len(data) {
		return 0, false
	}
	fields := strings.Fields(data[end+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil || ppid < 0 {
		return 0, false
	}
	return ppid, true
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
