//go:build !linux

package fetch

// limitFileSize has no hard limit here; the poll of the folder applies.
func limitFileSize(int, int64) error { return nil }

// killedForSize is false here: there is no file size limit to break.
func killedForSize(error) bool { return false }
