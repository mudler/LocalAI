package chv

import (
	"bytes"
	"regexp"
	"strconv"
	"time"
)

// Bins holds the paths of the host binaries. An empty path means a lookup
// in PATH.
type Bins struct {
	CloudHypervisor string // default "cloud-hypervisor"
	Virtiofsd       string // default "virtiofsd"
	Bwrap           string // default "bwrap"
	Mke2fs          string // default "mke2fs"
	Debugfs         string // default "debugfs"
}

// Options configures a Backend.
type Options struct {
	StateDir     string // session folders live under it
	CacheDir     string // converted images, by digest
	Bin          Bins
	DefaultImage string        // OCI reference with digest; empty means chv.DefaultImage
	ReadyTimeout time.Duration // default 10 s
	// NoBwrap runs virtiofsd and debugfs without bubblewrap. Set it only
	// when the backend runs in a container that already isolates them,
	// because bubblewrap cannot make namespaces there without extra
	// container privileges.
	NoBwrap bool
	// StageLimit bounds the file content that one checkpoint stages on the
	// host, in bytes. Zero means the size of the upper image. The checkpoint
	// passes it into the reader.
	StageLimit int64
}

// DefaultImage is the guest image of this SDK release, by digest. The release
// process sets it (see sandbox/README.md); Options.DefaultImage overrides it.
var DefaultImage = ""

// Minimum versions of the host binaries.
var minimums = map[string][2]int{
	"cloud-hypervisor": {54, 0},
	"virtiofsd":        {1, 13},
	"bwrap":            {0, 9},
	"mke2fs":           {1, 47},
	"debugfs":          {1, 47},
}

// Backend creates Cloud Hypervisor sandboxes.
type Backend struct {
	opts Options
	bins Bins
}

var versionRE = regexp.MustCompile(`\bv?(\d+)\.(\d+)`)

// parseVersion finds the first "major.minor" in the output of a version
// command: "cloud-hypervisor v54.0", "virtiofsd 1.14.0", "bubblewrap 0.11.1",
// "mke2fs 1.47.2 (1-Jan-2025)".
func parseVersion(out []byte) ([2]int, bool) {
	m := versionRE.FindSubmatch(firstLine(out))
	if m == nil {
		return [2]int{}, false
	}
	a, _ := strconv.Atoi(string(m[1]))
	b, _ := strconv.Atoi(string(m[2]))
	return [2]int{a, b}, true
}

func firstLine(b []byte) []byte {
	if before, _, ok := bytes.Cut(b, []byte{'\n'}); ok {
		return before
	}
	return b
}
