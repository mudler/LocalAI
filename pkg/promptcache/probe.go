package promptcache

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// ProbeSize is the size of the file the transfer probe writes and reads.
const ProbeSize = 64 << 20

// ErrNoCacheDrop means the platform gives no way to drop a file from the page
// cache, so a cold read cannot be measured.
var ErrNoCacheDrop = errors.New("promptcache: cannot drop the page cache on this platform")

// probeName is the probe file in the root. It matches no cache file pattern,
// so the janitor never touches it.
const probeName = ".read-probe"

// MeasureTransferSpeed measures how fast the tier at dir delivers bytes that
// are not in memory: it writes size bytes of random data, drops them from the
// page cache and reads them back. It returns MB/s (10^6 bytes per second).
// The backend compares the transfer time of an entry with the time to
// compute it again, so the same number can describe a later network tier.
// The file is removed before it returns.
func MeasureTransferSpeed(dir string, size int64) (float64, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	path := filepath.Join(dir, probeName+"."+strconv.Itoa(os.Getpid()))
	defer os.Remove(path)

	w, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	// Before the write too: darwin only keeps pages out of the cache that
	// are written after F_NOCACHE is set.
	if err := dropCache(w); err != nil {
		w.Close()
		return 0, err
	}
	// Random data, so a compressing filesystem cannot shrink the read.
	if _, err := io.CopyN(w, rand.Reader, size); err != nil {
		w.Close()
		return 0, err
	}
	if err := w.Sync(); err != nil {
		w.Close()
		return 0, err
	}
	if err := dropCache(w); err != nil {
		w.Close()
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}

	r, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	if err := dropCache(r); err != nil {
		return 0, err
	}
	buf := make([]byte, 1<<20)
	start := time.Now()
	n, err := io.CopyBuffer(io.Discard, r, buf)
	elapsed := time.Since(start)
	if err != nil {
		return 0, err
	}
	if n != size {
		return 0, fmt.Errorf("promptcache: probe read %d of %d bytes", n, size)
	}
	if elapsed <= 0 {
		elapsed = time.Nanosecond
	}
	return float64(n) / 1e6 / elapsed.Seconds(), nil
}

// speeds caches one transfer probe per root path for the life of the process.
var speeds = struct {
	sync.Mutex
	done    map[string]float64
	running map[string]bool
}{done: map[string]float64{}, running: map[string]bool{}}

// TransferSpeed returns the measured transfer speed of root in MB/s, and false
// when no probe has finished for it (not started, running, or failed).
func TransferSpeed(root string) (float64, bool) {
	speeds.Lock()
	defer speeds.Unlock()
	v, ok := speeds.done[filepath.Clean(root)]
	return v, ok
}

// StartTransferProbe measures the transfer speed of root in the background, once per
// path per process. It never blocks: a backend that starts before the probe
// ends runs without a speed. onDone, when not nil, gets the result.
func StartTransferProbe(root string, onDone func(mbps float64, err error)) {
	root = filepath.Clean(root)
	speeds.Lock()
	if _, ok := speeds.done[root]; ok || speeds.running[root] {
		speeds.Unlock()
		return
	}
	speeds.running[root] = true
	speeds.Unlock()

	go func() {
		mbps, err := MeasureTransferSpeed(root, ProbeSize)
		speeds.Lock()
		delete(speeds.running, root)
		if err == nil {
			speeds.done[root] = mbps
		}
		speeds.Unlock()
		if onDone != nil {
			onDone(mbps, err)
		}
	}()
}
