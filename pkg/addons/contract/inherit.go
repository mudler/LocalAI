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

// A package with osSettings.windows.execution "wsl2" runs in WSL 2, where no
// handle of the owner exists. The owner starts it through wsl.exe with
// ChannelEnv set to ChannelStdio, and the standard input and output of the
// process carry one multiplexed session (yamux):
//
//   - The first stream, opened by the owner, is the startup stream. The add-on
//     writes its process ID as one decimal line (at most MaxStdioHello bytes,
//     newline included), then reads the startup object, which has the same
//     content as on the other platforms. The end of the stream, or of the
//     session, is the signal to stop, as the end of the startup pipe is.
//   - Every later stream that the owner opens is one connection to the
//     protocol endpoint of the add-on.
//   - Every stream that the add-on opens is one connection to the host
//     operation endpoint. The SDK serves a 127.0.0.1 listener in WSL for it
//     and puts its address in the startup object as the host endpoint.
//
// Standard error stays the log stream. The SDK handles all of this; an add-on
// that uses protocol.StartupChannel and protocol.Listener needs no change.
const (
	// ChannelEnv names the variable that selects the channel mode.
	ChannelEnv = "LOCALAI_ADDON_CHANNEL"
	// ChannelStdio is the value of ChannelEnv for the multiplexed standard
	// streams.
	ChannelStdio = "stdio"
	// MaxStdioHello bounds the process ID line of the startup stream.
	MaxStdioHello = 24
)
