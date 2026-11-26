//go:build windows

package system

// ownerUID is -1 on Windows, which has no user IDs.
func ownerUID(string) int { return -1 }
