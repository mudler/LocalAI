//go:build !linux

// SPDX-License-Identifier: MIT
package xsysinfo

// ProcessVRAM is unavailable on platforms without Linux DRM fdinfo accounting.
func ProcessVRAM(pid int) (uint64, bool) {
	return 0, false
}
