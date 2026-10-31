// Package guestagent is the agent that runs as PID 1 in a sandbox guest, and
// the host side of its protocol.
//
// The host opens one connection per request to vsock port 1024 of the guest.
// Frames are length-prefixed: a 4-byte big-endian length, then a JSON object
// with a "type" field. A frame larger than MaxFrame is refused.
//
//	ready     guest -> host  version            (the guest dials host port 1024 once at boot)
//	exec      host -> guest  args, env, dir, tty
//	stdin     host -> guest  data; an empty data closes stdin
//	credit    guest -> host  n: stdin bytes the child took
//	signal    host -> guest  signal
//	resize    host -> guest  rows, cols
//	stdout    guest -> host  data
//	stderr    guest -> host  data
//	exit      guest -> host  code, signal
//	freeze, thaw, health, shutdown, detach, checkpoint   host -> guest
//	ok, error guest -> host  message on error
//
// Stdin is flow controlled: the host sends at most StdinWindow bytes before
// it waits for credit. Each chunk the guest writes into the child is
// acknowledged with a credit frame. Signal, resize and close frames do not
// count against the window.
//
// One exec connection carries one process. The guest answers ok when the
// process started, then streams output, then sends exit. When the host
// closes the connection before the exit, the guest kills the process group.
// A detach request marks the processes running at that moment: their
// connections may break (a snapshot restore resets every vsock connection)
// and they keep running with their output discarded.
//
// A guest that runs under gVisor has no vsock. There the agent listens on a
// Unix socket in a folder that the host bind-mounts into the sandbox (see
// CtlDir and SwitchListener), the checkpoint request closes that listener,
// and SIGUSR1 opens it again after a restore. The checkpoint request closes
// only the listener: connections that are open stay open. Before it sends the
// request the host must close its forwarded connections (or detach them), and
// when the checkpoint fails the host must send SIGUSR1 as well, so that the
// agent listens again.
//
// The package uses only the standard library. Boot, Spawner, Freezer and the
// vsock calls work only on Linux.
package guestagent
