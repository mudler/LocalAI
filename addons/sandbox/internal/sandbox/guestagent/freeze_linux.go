package guestagent

import (
	"errors"
	"log"
	"os"
	"sync"
	"syscall"
	"time"
)

// ioctl numbers from linux/fs.h: _IOWR('X', 119, int) and _IOWR('X', 120,
// int). They are the same on amd64 and arm64.
const (
	fiFreeze = 0xC0045877
	fiThaw   = 0xC0045878
)

// Freezer freezes the file system at Path, so the host can read the disk
// image while the guest runs.
type Freezer struct {
	Path string
	// AutoThaw thaws the file system when the host sends no thaw in time,
	// for example because the host process died. Zero means 60 s.
	AutoThaw time.Duration
	// Ioctl runs one ioctl on the opened Path. Nil means the real ioctl.
	Ioctl func(f *os.File, req uintptr) error

	mu     sync.Mutex
	frozen bool
	gen    uint64 // counts freezes: a timer of an older freeze does nothing
	timer  *time.Timer
}

// ErrFrozen means a freeze while the file system is frozen.
var ErrFrozen = errors.New("guestagent: already frozen")

func realIoctl(f *os.File, req uintptr) error { return fileIoctl(f, req, nil) }

func (z *Freezer) ioctl(req uintptr) error {
	f, err := os.Open(z.Path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fn := z.Ioctl
	if fn == nil {
		fn = realIoctl
	}
	return fn(f, req)
}

// Freeze writes dirty data, then freezes the file system.
func (z *Freezer) Freeze() error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.frozen {
		return ErrFrozen
	}
	syscall.Sync()
	if err := z.ioctl(fiFreeze); err != nil {
		return err
	}
	z.frozen = true
	z.gen++
	z.arm(z.gen)
	return nil
}

// arm starts the auto-thaw timer of freeze g. The caller holds z.mu.
func (z *Freezer) arm(g uint64) {
	d := z.AutoThaw
	if d == 0 {
		d = 60 * time.Second
	}
	z.timer = time.AfterFunc(d, func() { z.autoThaw(g) })
}

// autoThaw thaws freeze g when it is still the current one. A failed thaw
// re-arms the timer, so the guest is not left frozen.
func (z *Freezer) autoThaw(g uint64) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if !z.frozen || z.gen != g {
		return
	}
	if err := z.thaw(); err != nil {
		log.Printf("auto-thaw of %s failed, retrying: %v", z.Path, err)
		z.arm(g)
	}
}

// Frozen reports whether the file system is frozen.
func (z *Freezer) Frozen() bool {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.frozen
}

// Thaw thaws the file system. It does nothing when it is not frozen. A
// failed thaw keeps the auto-thaw timer, so the guest still thaws later.
func (z *Freezer) Thaw() error {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.thaw()
}

// thaw does the thaw. The caller holds z.mu.
func (z *Freezer) thaw() error {
	if !z.frozen {
		return nil
	}
	if err := z.ioctl(fiThaw); err != nil {
		return err
	}
	if z.timer != nil {
		z.timer.Stop()
	}
	z.frozen = false
	return nil
}
