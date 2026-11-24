// Package gvisor is the gVisor (runsc) backend of the sandbox package. It
// runs the same guest image and the same guest agent as the chv backend on a
// host without KVM, with weaker limits: gVisor is a user-space kernel, not a
// virtual machine, and the memory and disk limits are watched, not enforced
// by the kernel.
//
// runsc and every process it starts run as the calling user. The one
// administrator step is an AppArmor profile that names the pinned runsc
// binary and allows user namespaces for it (see Profile).
//
// Options.StateDir holds one folder per sandbox and the lock file .lock (see
// New). Layout of a sandbox folder:
//
//	state.json   view, actor, base revision, own revisions, index, image, limits, ports, epoch
//	lower/       the snapshot of the view (read-only in the sandbox)
//	upper/       plain host folder; the agent makes data/ (the upper of /workspace), work/ and home/ in it
//	ctl/         agent.sock and port-<n>.sock
//	bundle/      the OCI config.json
//	runsc/       the runsc --root state
//	checkpoint/  the runsc checkpoint image while suspended
//	pid          the pid of the runsc sandbox process
//	console.log  standard output and error of the guest agent
package gvisor
