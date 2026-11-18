// SPDX-License-Identifier: MIT
package contract

// On Linux and macOS the owner hands an add-on process two inherited
// descriptors: descriptor 3 is a listener the owner already bound to
// 127.0.0.1, and descriptor 4 is the read end of the startup channel.
//
// Windows cannot pass a numbered descriptor, and a listening socket cannot be
// turned back into a listener in the child. There the owner passes two
// inherited pipe handles instead, and names them in the environment of the
// child. The child binds its own listener to 127.0.0.1 on an ephemeral port,
// writes the address as one line (for example "127.0.0.1:49152\n") on the
// endpoint pipe and closes it. Only then does the owner write the startup
// object, which has the same content as on the other platforms, on the
// startup pipe. A child that reports anything other than a 127.0.0.1 address
// is stopped.
const (
	// StartupHandleEnv names the variable that holds the numeric handle of
	// the startup pipe (read end) of the child.
	StartupHandleEnv = "LOCALAI_ADDON_STARTUP_HANDLE"
	// EndpointHandleEnv names the variable that holds the numeric handle of
	// the endpoint pipe (write end) of the child.
	EndpointHandleEnv = "LOCALAI_ADDON_ENDPOINT_HANDLE"
	// MaxEndpointReport bounds the endpoint line, newline included.
	MaxEndpointReport = 64
)
