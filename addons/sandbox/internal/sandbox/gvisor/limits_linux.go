//go:build linux

package gvisor

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The limits are best effort, weaker than the VM's (owner decision):
//
//   - CPUs: runsc runs under taskset with Spec.CPUs CPUs (Backend.start).
//   - Memory: watchMemory reads the sandbox's memory use from "runsc usage"
//     every second; three readings in a row over Spec.Memory stop the sandbox.
//   - Disk: every process has RLIMIT_FSIZE = Spec.Disk (bundleConfig), and
//     watchDisk adds up the size of upper/ every 10 seconds; over Spec.Disk,
//     or over maxUpperEntries entries, Exec returns ErrDiskFull.

// memoryReadings is how many readings in a row over the limit stop the sandbox.
const memoryReadings = 3

func (s *sbx) watchMemory(in *inst, limit int64) {
	t := time.NewTicker(s.b.memEvery)
	defer t.Stop()
	over := 0
	for {
		select {
		case <-in.quit:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := s.b.usage(ctx, s.runscRoot(), s.id)
		cancel()
		if err != nil {
			over = 0 // "in a row" means no failed reading in between
			continue // the alive watcher handles a sandbox that is gone
		}
		if n > limit {
			over++
		} else {
			over = 0
		}
		if over >= memoryReadings {
			log.Printf("gvisor: sandbox %s uses %d bytes, limit %d", s.id, n, limit)
			s.stopInst(in)
			s.markGone(in, "memory limit")
			return
		}
	}
}

func (s *sbx) watchDisk(in *inst, limit int64) {
	t := time.NewTicker(s.b.diskEvery)
	defer t.Stop()
	for {
		select {
		case <-in.quit:
			return
		case <-t.C:
		}
		s.measureDisk(in, limit)
	}
}

// measureDisk adds up the upper layer and sets or clears the full flag. The
// layer is full when it uses more than limit bytes, or has more entries than
// a checkpoint can read (maxUpperEntries): empty files use no blocks, but a
// checkpoint of them fails. A measurement of an instance that has stopped
// since it started is dropped: a refresh may have cleared the flag for the
// fresh layer. in is nil when no instance is known.
func (s *sbx) measureDisk(in *inst, limit int64) {
	used, capped := dirUsage(filepath.Join(s.dir, "upper"), maxUpperEntries)
	if in != nil {
		select {
		case <-in.quit:
			return
		default:
		}
	}
	s.diskFull.Store(capped || used > limit)
}

// dirUsage adds up the disk blocks that the files below root use. It stops at
// maxEntries entries and reports capped. Errors of entries that change or
// vanish while it walks are ignored.
func dirUsage(root string, maxEntries int) (n int64, capped bool) {
	count := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if count++; count > maxEntries {
			capped = true
			return fs.SkipAll
		}
		info, err := os.Lstat(p)
		if err != nil {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			n += st.Blocks * 512
		}
		return nil
	})
	return n, capped
}
