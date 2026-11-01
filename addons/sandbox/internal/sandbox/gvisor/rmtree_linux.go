//go:build linux

package gvisor

import "github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"

// removeAll deletes dir and everything below it. A command in the sandbox can
// make a folder it cannot write to, which os.RemoveAll cannot empty;
// ociimage.RemoveTree makes the folders writable when the first try fails.
func removeAll(dir string) error { return ociimage.RemoveTree(dir) }
