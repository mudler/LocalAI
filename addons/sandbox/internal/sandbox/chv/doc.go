// Package chv is the Cloud Hypervisor backend of package sandbox.
//
// Each sandbox is one Cloud Hypervisor microVM. The guest boots a root image
// converted from an OCI image, sees the workspace view through virtiofs as
// the read-only lower layer, and writes into a per-sandbox ext4 upper image.
// The guest has no network device: it reaches the host only through vsock.
// The host reads the upper image with debugfs while the guest file system is
// frozen. Nothing runs as root on the host.
//
// Host requirements: /dev/kvm (read and write), cloud-hypervisor 54 or later,
// virtiofsd 1.13 or later, bubblewrap 0.9 or later (unless Options.NoBwrap)
// and e2fsprogs 1.47 or later (mke2fs and debugfs). New checks them and
// returns sandbox.ErrUnsupported with the first missing piece.
//
// Session folder, under Options.StateDir:
//
//	<id>/lower/         read-only hard-link tree from Materialize
//	<id>/upper.ext4     sparse ext4 without a journal
//	<id>/chv.sock       Cloud Hypervisor API socket
//	<id>/vsock.sock     hybrid vsock socket; vsock.sock_<port> are host listeners
//	<id>/vfs/           virtiofsd socket
//	<id>/stage/         file contents read from the upper image, during a checkpoint
//	<id>/state.json     view, actor, base, own revisions, upper index, image
//	<id>/snapshot/      only after Suspend
//
// Image cache, under Options.CacheDir: <digest>-<arch>/root.ext4, vmlinux and
// env.json.
package chv
