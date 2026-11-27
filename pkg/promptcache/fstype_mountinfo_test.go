package promptcache_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/promptcache"
)

var _ = Describe("the FUSE mounts", func() {
	const mountinfo = `22 1 0:21 / / rw,relatime - overlay overlay rw
30 22 0:40 / /models rw,relatime - fuse.sshfs host:/models rw,user_id=0
31 22 0:41 / /data rw,relatime - virtiofs data rw
32 22 0:42 / /mnt/win rw,relatime - fuseblk /dev/sdb1 rw
33 22 0:43 / /host/my\040files rw,relatime - fuse.grpcfuse grpcfuse rw
34 30 0:44 / /models/local rw,relatime - ext4 /dev/sda1 rw`

	It("finds the mount that holds a path, the longest mount point first", func() {
		Expect(promptcache.MountFSType(mountinfo, "/models/qwen")).To(Equal("fuse.sshfs"))
		Expect(promptcache.MountFSType(mountinfo, "/models/local/cache")).To(Equal("ext4"))
		Expect(promptcache.MountFSType(mountinfo, "/data/prompt-cache")).To(Equal("virtiofs"))
		Expect(promptcache.MountFSType(mountinfo, "/modelsx")).To(Equal("overlay"), "a name prefix is not a mount")
		Expect(promptcache.MountFSType(mountinfo, "/host/my files/cache")).To(Equal("fuse.grpcfuse"), "escaped spaces")
		Expect(promptcache.MountFSType("", "/x")).To(BeEmpty())
	})

	It("counts only remote FUSE filesystems as network disks", func() {
		Expect(promptcache.FuseIsNetwork("fuse.sshfs")).To(BeTrue())
		Expect(promptcache.FuseIsNetwork("fuse.s3fs")).To(BeTrue())
		Expect(promptcache.FuseIsNetwork("fuse.rclone")).To(BeTrue())
		Expect(promptcache.FuseIsNetwork("virtiofs")).To(BeFalse(), "Docker Desktop and VM shares of a local disk")
		Expect(promptcache.FuseIsNetwork("fuseblk")).To(BeFalse(), "ntfs-3g and other block-device FUSE")
		Expect(promptcache.FuseIsNetwork("fuse.grpcfuse")).To(BeFalse(), "Docker Desktop file sharing")
		Expect(promptcache.FuseIsNetwork("fuse.unknownfs")).To(BeFalse(), "unknown: the speed gate decides")
	})
})
