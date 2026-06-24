package xsysinfo

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shirou/gopsutil/v3/disk"
)

// DiskInfo describes the filesystem backing a particular path.
type DiskInfo struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
}

// GetDiskInfo reports usage for the filesystem that holds `path`.
//
// The path matters. A worker stages model weights into its models directory,
// which is very often a separate mount (an NVMe volume, a network share) from
// `/`. Measuring the root filesystem would answer a question nobody asked and
// would have missed the incident this exists for, where the models volume was
// the one at 100%.
//
// A path that does not exist yet is normal on a fresh worker: the models
// directory is created lazily. Rather than failing, walk up to the nearest
// existing ancestor, which sits on the same filesystem the directory will be
// created on in all but pathological setups.
func GetDiskInfo(path string) (*DiskInfo, error) {
	if path == "" {
		return nil, fmt.Errorf("no path given")
	}
	target, err := nearestExistingDir(path)
	if err != nil {
		return nil, err
	}
	usage, err := disk.Usage(target)
	if err != nil {
		return nil, fmt.Errorf("reading disk usage for %s: %w", target, err)
	}
	return &DiskInfo{
		Total: usage.Total,
		Used:  usage.Used,
		// Free, not (Total - Used): on ext4 the reserved-blocks pool sits
		// between them, and writing into it is exactly what fails with
		// ENOSPC for a non-root process.
		Available: usage.Free,
	}, nil
}

// nearestExistingDir walks up from path until it finds something that exists.
func nearestExistingDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	for {
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no existing ancestor of %s", path)
		}
		abs = parent
	}
}
