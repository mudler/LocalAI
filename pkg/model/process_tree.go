package model

// processTree tracks the OS state that lets LocalAI reap a backend's entire
// process tree when the backend is stopped. The mechanics are platform
// specific and chosen at build time: process_tree_windows.go wraps the tree in
// a job object, while process_tree_other.go is a no-op because the process
// manager already owns the tree elsewhere.
//
// On Windows a backend is started through run.ps1, so go-processmanager tracks
// only the PowerShell wrapper's PID; without a job object owning the tree an
// abrupt stop (or local-ai.exe dying) would orphan the real backend binary.
type processTree interface {
	// assign captures the process identified by pid so a later terminate reaps
	// it together with any children it spawns afterwards.
	assign(pid int) error
	// terminate kills the tracked process tree and releases its handles.
	terminate()
}
