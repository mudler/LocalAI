//go:build darwin

// SPDX-License-Identifier: MIT
package runtime

// wslSpecMode does nothing on macOS: the stand-in needs a Linux executable.
func wslSpecMode(*Supervisor) {}
