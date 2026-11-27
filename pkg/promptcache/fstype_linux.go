//go:build linux

package promptcache

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// networkMagic maps the statfs f_type of the network, cluster and FUSE
// filesystems of Linux to a name. FUSE covers sshfs, s3fs, rclone and every
// other FUSE filesystem: most of them are remote, and statfs cannot tell
// the local ones apart.
var networkMagic = map[uint32]string{
	0x6969:     "nfs",
	0x517b:     "smb",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x65735546: "fuse",
	0x00c36400: "ceph",
	0x01021997: "9p",
	0x5346414f: "afs",
	0x6b414653: "kafs",
	0x73757245: "coda",
	0x564c:     "ncp",
	0x0bd00bd0: "lustre",
	0x47504653: "gpfs",
	0x20030528: "orangefs",
	0x01161970: "gfs2",
	0x7461636f: "ocfs2",
	0x19830326: "fhgfs",
}

// localMagic names a few common local filesystems, for the logs.
var localMagic = map[uint32]string{
	0xef53:     "ext4",
	0x58465342: "xfs",
	0x9123683e: "btrfs",
	0x01021994: "tmpfs",
	0x794c7630: "overlay",
	0x2fc12fc1: "zfs",
	0xf2f52010: "f2fs",
}

// fsTypeOf names the filesystem from its f_type magic number and says if it
// is a network one. The conversion to uint32 makes the magic numbers equal on
// every architecture: the field is int64 on some and int32 or uint32 on
// others.
func fsTypeOf(fs *unix.Statfs_t) (string, bool) {
	magic := uint32(fs.Type)
	if name, ok := networkMagic[magic]; ok {
		return name, true
	}
	if name, ok := localMagic[magic]; ok {
		return name, false
	}
	return fmt.Sprintf("0x%x", magic), false
}
