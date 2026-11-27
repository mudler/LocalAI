//go:build darwin

package promptcache

import (
	"strings"

	"golang.org/x/sys/unix"
)

// networkTypes are the f_fstypename values of the network and FUSE
// filesystems of macOS.
var networkTypes = map[string]bool{
	"nfs": true, "smbfs": true, "afpfs": true, "webdav": true, "cifs": true,
	"osxfuse": true, "macfuse": true, "fusefs": true,
}

// fsTypeOf names the filesystem from f_fstypename and says if it is a
// network one.
func fsTypeOf(_ string, fs *unix.Statfs_t) (string, bool) {
	name := unix.ByteSliceToString(fs.Fstypename[:])
	lower := strings.ToLower(name)
	return name, networkTypes[lower] || strings.HasPrefix(lower, "osxfuse") || strings.HasPrefix(lower, "macfuse")
}
