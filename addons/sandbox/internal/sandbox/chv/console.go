package chv

import (
	"io"
	"os"
	"strings"
	"time"
)

// kernelPanicText is the line that the kernel prints on a panic that nothing
// recovers, for example when init exits. The guest keeps the default
// panic=0, so the VM stays up with the line in the console log.
const kernelPanicText = "Kernel panic - not syncing"

// consoleHasPanic reports whether the end of the console log holds the
// kernel panic line.
func consoleHasPanic(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	const n = 64 << 10
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	return strings.Contains(string(b), kernelPanicText)
}

// consoleLimit is the size at which the post-boot watcher treats the console
// log as a flood. A guest process cannot reach the console (the agent removes
// the nodes), but a kernel printk flood or a bug could still fill the host
// disk. consoleKeep is what stays of the log for diagnosis.
const (
	consoleLimit = 16 << 20
	consoleKeep  = 64 << 10
)

// watchConsole calls onPanic once when the console log shows a kernel panic,
// and onFlood once when the log is larger than limit (0 means no cap). It
// returns after either, or when done closes.
func watchConsole(path string, done <-chan struct{}, every time.Duration, limit int64, onPanic, onFlood func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if limit > 0 && onFlood != nil {
				if st, err := os.Stat(path); err == nil && st.Size() > limit {
					onFlood()
					return
				}
			}
			if consoleHasPanic(path) {
				onPanic()
				return
			}
		}
	}
}

// trimConsole cuts the log in place to its last keep bytes. The VM must be
// stopped: nothing else writes the file.
func trimConsole(path string, keep int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() <= keep {
		return nil
	}
	buf := make([]byte, keep)
	if _, err := f.ReadAt(buf, st.Size()-keep); err != nil && err != io.EOF {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.WriteAt(buf, 0)
	return err
}
