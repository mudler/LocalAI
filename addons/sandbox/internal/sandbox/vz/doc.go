// Package vz is the Virtualization.framework backend of package sandbox, for
// Apple silicon Macs.
//
// Each sandbox is one Linux VM that runs in a pure-Go process through
// package vmm. The guest boots the same arm64 kernel, root image and guest
// agent as the Cloud Hypervisor backend. It sees the workspace view through
// virtio-fs as the read-only lower layer and writes into a per-sandbox ext4
// upper image on a virtio block device. The guest has no network device: it
// reaches the host only through virtio sockets. The host reads the upper
// image with package ext4ro while the guest file system is frozen. Nothing
// runs as root on the host.
//
// Host requirements: macOS on Apple silicon with Virtualization.framework,
// and a process that holds the com.apple.security.virtualization
// entitlement. New checks them and returns sandbox.ErrUnsupported with the
// first missing piece.
//
// A VM belongs to the process that started it. When that process exits for
// any reason, every VM stops and nothing can attach to it again. New
// therefore removes the session folders that an earlier process left.
//
// Suspend does not save guest memory: macOS does not allow it for this
// process model. Suspend checkpoints the upper layer and stops the VM.
// Resume boots a new VM over the same files. Processes do not survive.
//
// Session folder, under Options.StateDir:
//
//	<id>/lower/         read-only hard-link tree from Materialize
//	<id>/upper.ext4     sparse ext4 without a journal
//	<id>/console.log    console of the guest
//	<id>/stage/         file contents read from the upper image, during a checkpoint
//	<id>/state.json     view, actor, base, own revisions, upper index, image
//
// Image cache, under Options.CacheDir: <digest>-<arch>/root.ext4, vmlinux and
// env.json.
package vz
