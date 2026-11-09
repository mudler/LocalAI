//go:build !windows

package model

// noopProcessTree satisfies processTree off Windows. There the process manager
// starts the backend directly and owns its whole tree, so no extra tracking is
// needed to reap it on stop.
type noopProcessTree struct{}

func newProcessTree() processTree { return noopProcessTree{} }

func (noopProcessTree) assign(int) error { return nil }

func (noopProcessTree) terminate() {}
