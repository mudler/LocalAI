package promptcache

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/xlog"
)

// Free-space watermarks. The backend applies the same constants before it
// writes (prompt_cache_policy.h), so both sides agree on when the disk is too
// full for the cache.
const (
	// LowWatermarkPercent and LowWatermarkMinBytes make the low watermark:
	// max(15% of the disk, 10 GiB) free. Below it the cache is paused and
	// shrinks until the disk has that much free again.
	LowWatermarkPercent  = 15
	LowWatermarkMinBytes = uint64(10) << 30
	// CriticalWatermarkPercent is the critical watermark: 5% of the disk
	// free. Below it the cache is disabled until the free space rises above
	// the low watermark again.
	CriticalWatermarkPercent = 5
)

// LowWatermark is the free space under which the cache pauses.
func LowWatermark(total uint64) uint64 {
	return max(total/100*LowWatermarkPercent, LowWatermarkMinBytes)
}

// CriticalWatermark is the free space under which the cache is disabled.
func CriticalWatermark(total uint64) uint64 {
	return total / 100 * CriticalWatermarkPercent
}

// Watermarks are the low and critical watermarks of a filesystem of total
// bytes: lowOverride and criticalOverride when not 0, else the default rule.
// The critical watermark is clamped to the low one.
func Watermarks(total, lowOverride, criticalOverride uint64) (low, critical uint64) {
	low, critical = LowWatermark(total), CriticalWatermark(total)
	if lowOverride > 0 {
		low = lowOverride
	}
	if criticalOverride > 0 {
		critical = criticalOverride
	}
	return low, min(critical, low)
}

// FSStats are the numbers of one filesystem.
type FSStats struct {
	Free   uint64 // bytes an unprivileged process may still write
	Total  uint64
	Device uint64 // id of the filesystem; two paths with the same id share it
	// FSType names the type of the filesystem when the platform tells it,
	// else it is empty.
	FSType string
	// Network is true when the filesystem is a network or FUSE filesystem
	// (NFS, SMB, sshfs, s3fs, Ceph...). The disk tier is off on such a
	// root: its files would cross the network on every load, and the
	// locking and rename rules the cache needs may not hold. False when the
	// type is local or unknown.
	Network bool
}

// StatProvider reads the filesystem of a path. Tests use a fake.
type StatProvider interface {
	Stat(path string) (FSStats, error)
}

// State is the state of the disk tier on this node.
type State string

const (
	// StateOn: the disk has room and the cache works.
	StateOn State = "on"
	// StatePaused: the free space is under the low watermark. Nothing new
	// is written and the oldest files go until the free space is back.
	StatePaused State = "paused_low_disk"
	// StateDisabled: the free space is under the critical watermark. The
	// cache stays off until the free space is above the low watermark.
	StateDisabled State = "disabled_disk_full"
	// StateUnknown: the filesystem could not be read.
	StateUnknown State = "unknown"
)

// nextState applies the watermarks with hysteresis: a disabled cache comes
// back only above the low watermark, not when it leaves the critical zone.
func nextState(prev State, free, low, critical uint64) State {
	switch {
	case free < critical:
		return StateDisabled
	case prev == StateDisabled && free < low:
		return StateDisabled
	case free < low:
		return StatePaused
	}
	return StateOn
}

// Status is what the last sweep saw.
type Status struct {
	State State `json:"state"`
	// CacheBytes is what the cache holds after the sweep.
	CacheBytes int64 `json:"cache_bytes"`
	// FreeBytes and TotalBytes are the numbers of the filesystem that holds
	// the cache (the models filesystem when they share it).
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
	// LowWatermarkBytes and CriticalWatermarkBytes are the thresholds of
	// that filesystem.
	LowWatermarkBytes      uint64 `json:"low_watermark_bytes"`
	CriticalWatermarkBytes uint64 `json:"critical_watermark_bytes"`
	// MaxBytes is the configured cap, EffectiveCapBytes the cap the sweep
	// enforced: never more than the cache plus the free space over the low
	// watermark, so the cache never fills the disk.
	MaxBytes          int64 `json:"max_bytes"`
	EffectiveCapBytes int64 `json:"effective_cap_bytes"`
	// SharesModelsDisk is true when the models are on the same filesystem.
	SharesModelsDisk bool `json:"shares_models_disk"`
	// Removed is what the sweep deleted.
	Removed Result `json:"removed"`
}

// Janitor sweeps one root and remembers the disk state between sweeps, for
// the hysteresis and to log a transition once.
type Janitor struct {
	// Store holds the files; NewDiskStore(Root) when nil.
	Store Store
	// Root is the directory of the store on this node, for the numbers of
	// its filesystem.
	Root string
	// ModelsPath is the models directory of the node. When it is on the
	// same filesystem as Root, its numbers decide: the cache must never
	// compete with model downloads.
	ModelsPath string
	Stats      StatProvider

	mu   sync.Mutex
	last Status
}

// ExistingDir is path or its nearest parent that exists, so a root that the
// backend did not create yet still has a filesystem.
func ExistingDir(path string) string {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return p
		}
		if filepath.Dir(p) == p {
			return p
		}
	}
}

func (j *Janitor) store() Store {
	if j.Store != nil {
		return j.Store
	}
	return NewDiskStore(j.Root)
}

// Sweep enforces p, with p.MaxBytes as the configured cap (0 for none), and
// the free-space watermarks of the filesystem on top.
func (j *Janitor) Sweep(ctx context.Context, p Policy) (Status, error) {
	s := j.store()
	entries, err := s.List(ctx, "")
	if err != nil {
		return j.Last(), err
	}

	st := Status{State: StateUnknown, MaxBytes: p.MaxBytes}
	var fsStats FSStats
	haveStats := false
	if j.Stats != nil {
		if s, err := j.Stats.Stat(ExistingDir(j.Root)); err == nil {
			fsStats, haveStats = s, true
			if j.ModelsPath != "" {
				if m, err := j.Stats.Stat(ExistingDir(j.ModelsPath)); err == nil && m.Device == s.Device {
					fsStats = m
					st.SharesModelsDisk = true
				}
			}
		}
	}

	j.mu.Lock()
	prev := j.last.State
	j.mu.Unlock()

	if haveStats {
		low, critical := Watermarks(fsStats.Total, p.LowFreeBytes, p.CriticalFreeBytes)
		st.FreeBytes, st.TotalBytes = fsStats.Free, fsStats.Total
		st.LowWatermarkBytes, st.CriticalWatermarkBytes = low, critical
		st.State = nextState(prev, fsStats.Free, low, critical)
	}

	st.Removed = sweepEntries(ctx, s, entries, p, func(live, removed int64) int64 {
		capBytes := int64(-1)
		if p.MaxBytes > 0 {
			capBytes = p.MaxBytes
		}
		if haveStats {
			// The cache may grow only into the free space over the low
			// watermark; under it, it shrinks by what is missing.
			// The free space was read before the sweep: add what the
			// time to live and the quotas freed since.
			room := live + int64(fsStats.Free) + removed - int64(st.LowWatermarkBytes)
			room = max(room, 0)
			if capBytes < 0 || room < capBytes {
				capBytes = room
			}
		}
		st.EffectiveCapBytes = max(capBytes, 0)
		return capBytes
	})

	if haveStats {
		// What the sweep deleted is free now.
		st.FreeBytes += uint64(st.Removed.Bytes)
	}
	if u, err := s.Stat(ctx); err == nil {
		st.CacheBytes = u.Bytes
	}

	if st.State != prev {
		switch st.State {
		case StateDisabled:
			xlog.Warn("prompt cache disk tier disabled: the disk is almost full",
				"root", j.Root, "free_bytes", st.FreeBytes, "critical_bytes", st.CriticalWatermarkBytes)
		case StatePaused:
			xlog.Info("prompt cache disk tier paused: low disk space",
				"root", j.Root, "free_bytes", st.FreeBytes, "low_bytes", st.LowWatermarkBytes)
		case StateOn:
			if prev != "" && prev != StateUnknown {
				xlog.Info("prompt cache disk tier back on", "root", j.Root, "free_bytes", st.FreeBytes)
			}
		}
	}

	j.mu.Lock()
	j.last = st
	j.mu.Unlock()
	return st, nil
}

// Last is the status of the last sweep.
func (j *Janitor) Last() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.last
}

// NodeUsage is what one node reports about its disk tier: the files it holds
// and the state of its disk at the last sweep. A node sends it as is.
type NodeUsage struct {
	Usage
	Status Status `json:"status"`
}

// Usage reports what the root holds now, with the status of the last sweep.
func (j *Janitor) Usage(ctx context.Context) (NodeUsage, error) {
	u, err := j.store().Stat(ctx)
	return NodeUsage{Usage: u, Status: j.Last()}, err
}
