package promptcache

import (
	"path/filepath"
	"strings"
)

// networkFuse lists the FUSE filesystems that read and write over the
// network. A FUSE mount of another kind (virtiofs, fuseblk, ntfs-3g, the file
// sharing of Docker Desktop) is on a local disk of the host; the transfer
// speed gate still guards a slow one.
var networkFuse = map[string]bool{
	"sshfs": true, "s3fs": true, "rclone": true, "gcsfuse": true, "goofys": true,
	"geesefs": true, "juicefs": true, "mountpoint-s3": true, "blobfuse": true,
	"blobfuse2": true, "ceph-fuse": true, "glusterfs": true, "lizardfs": true,
	"mfs": true, "seaweedfs": true, "alluxio-fuse": true, "curlftpfs": true,
	"smbnetfs": true, "davfs": true, "s3backer": true, "objectivefs": true,
	"gvfsd-fuse": true,
}

// FuseIsNetwork reports whether a mount of this type (from mountinfo, for
// example "fuse.sshfs" or "fuseblk") is a FUSE filesystem on the network.
func FuseIsNetwork(fsType string) bool {
	sub, ok := strings.CutPrefix(fsType, "fuse.")
	if !ok {
		return false // fuseblk, virtiofs, plain fuse with no subtype
	}
	return networkFuse[sub]
}

// MountFSType returns the filesystem type of the mount that holds path, from
// the text of /proc/self/mountinfo: the mount point that is the longest
// prefix of path wins. "" when no mount matches.
func MountFSType(mountinfo, path string) string {
	path = filepath.Clean(path)
	best, bestType := -1, ""
	for _, line := range strings.Split(mountinfo, "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		f := strings.Fields(pre)
		g := strings.Fields(post)
		if len(f) < 5 || len(g) < 1 {
			continue
		}
		mp := unescapeMount(f[4])
		if !underMount(path, mp) || len(mp) <= best {
			continue
		}
		best, bestType = len(mp), g[0]
	}
	return bestType
}

func underMount(path, mp string) bool {
	if mp == "/" {
		return true
	}
	return path == mp || strings.HasPrefix(path, mp+"/")
}

// unescapeMount undoes the octal escapes of mountinfo (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }
