package vz

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
)

// Options configures a Backend.
type Options struct {
	StateDir     string // session folders live under it
	CacheDir     string // converted images, by digest
	DefaultImage string // OCI reference with digest; empty means vz.DefaultImage
	// ReadyTimeout is how long Create, Refresh and Resume wait for the guest.
	// The default is 10 s.
	ReadyTimeout time.Duration
	// StageLimit bounds the file content that one checkpoint stages on the
	// host, in bytes. Zero means the size of the upper image.
	StageLimit int64
	// KeepSuspended keeps the suspended sandboxes of an earlier process when
	// New sweeps StateDir. Leave it off unless the caller stores snapshot
	// references across restarts: otherwise nobody can resume them and their
	// files fill the disk.
	KeepSuspended bool
}

// DefaultImage is the guest image of this SDK release, by digest. The release
// process sets it; Options.DefaultImage overrides it.
var DefaultImage = ""

// Backend creates Virtualization.framework sandboxes.
type Backend struct {
	opts Options
	mk   machineFactory
	// loadImage replaces the image conversion in tests. Nil means ext4image.
	loadImage func(ctx context.Context, ref string) (guestImage, error)

	// host holds the limits of this Mac. Zero values mean no limit (tests).
	host vmm.HostLimits

	lockMu sync.Mutex
	lock   *os.File // the exclusive lock on StateDir, held until Close

	liveMu sync.Mutex
	live   map[*vmHandle]struct{} // the VMs that run now
}

// Close releases the lock on StateDir, so that another New can use the folder.
// It stops the VMs that the backend started, so that no VM runs without a
// backend that watches it. The session folders stay: the next New on the same
// StateDir removes them (the suspended ones too, unless KeepSuspended is
// set). Close is safe to call twice.
func (b *Backend) Close() error {
	if b == nil {
		return nil
	}
	b.stopAll()
	b.lockMu.Lock()
	defer b.lockMu.Unlock()
	if b.lock == nil {
		return nil
	}
	f := b.lock
	b.lock = nil
	return f.Close()
}
