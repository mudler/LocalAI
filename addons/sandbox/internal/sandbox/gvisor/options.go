package gvisor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// MinRelease is the oldest runsc release this backend supports.
const MinRelease = "20261012.0"

// Options configures a Backend.
type Options struct {
	StateDir     string        // one folder per sandbox
	CacheDir     string        // unpacked images, by digest
	Runsc        string        // path to runsc; its gvisor-bin/ folder is next to it
	DefaultImage string        // OCI reference with digest
	ReadyTimeout time.Duration // default 10 s
	// StageLimit bounds the file content that one checkpoint stages on the
	// host, in bytes. Zero means twice Spec.Disk of the sandbox: the disk limit
	// is watched and not enforced, so the upper layer can pass it, and a
	// checkpoint must still be able to save that work.
	StageLimit int64
	// KeepSuspended keeps the suspended sandboxes of an earlier process when
	// New sweeps StateDir. Leave it off unless the caller stores snapshot
	// references across restarts: otherwise nobody can resume them and their
	// checkpoints fill the disk.
	KeepSuspended bool
}

// Backend creates gVisor sandboxes.
type Backend struct {
	opts    Options
	runsc   string // absolute path
	taskset string // absolute path
	cpus    []int  // the CPUs this process may use
	next    atomic.Uint32
	lockMu  sync.Mutex
	lock    *os.File // the exclusive lock on StateDir, held until Close

	// Intervals of the limit watchers. New sets them; tests shorten them.
	memEvery, diskEvery, aliveEvery time.Duration
}

// Close releases the lock on StateDir, so that another New can use the
// folder. It does not stop sandboxes: they outlive the backend, and the next
// New on the same StateDir stops them (the suspended ones too, unless
// KeepSuspended is set). Close is
// safe to call twice.
func (b *Backend) Close() error {
	if b == nil {
		return nil
	}
	b.lockMu.Lock()
	defer b.lockMu.Unlock()
	if b.lock == nil {
		return nil
	}
	f := b.lock
	b.lock = nil
	return f.Close()
}

// ValidateProfilePath reports whether p can be the subject of an AppArmor
// profile line: an absolute, clean, valid UTF-8 path without control
// characters (newline, NUL), quotes, backslashes, braces, brackets or the
// glob characters * ? ^. Such characters would break the profile line or make
// it name more than one binary.
func ValidateProfilePath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return fmt.Errorf("gvisor: profile path %q is not an absolute, clean path", p)
	}
	if !utf8.ValidString(p) {
		return errors.New("gvisor: profile path is not valid UTF-8")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || !unicode.IsPrint(r) || strings.ContainsRune("\"\\{}[]*?^", r) {
			return fmt.Errorf("gvisor: profile path %q has a character that an AppArmor profile cannot name safely", p)
		}
	}
	return nil
}

// Profile returns the AppArmor profile that lets the runsc binary at path
// create user namespaces. The caller must check path with ValidateProfilePath
// first; Profile does not. An administrator saves the text to a file and loads it with
// "apparmor_parser -r <file>". The profile names only that binary, so the
// restriction stays on for every other program. Each runsc upgrade that
// changes the path needs its own profile.
func Profile(runsc string) string {
	sum := sha256.Sum256([]byte(runsc))
	return fmt.Sprintf("abi <abi/4.0>,\ninclude <tunables/global>\n\nprofile localai-runsc-%s %q flags=(unconfined) {\n  userns,\n}\n",
		hex.EncodeToString(sum[:4]), runsc)
}
