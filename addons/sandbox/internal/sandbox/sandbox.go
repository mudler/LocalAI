package sandbox

import coresandbox "github.com/mudler/LocalAI/core/services/studio/sandbox"

// The interfaces and types of a sandbox are the ones of the chats
// (core/services/studio/sandbox): the add-on's backends implement them, and
// the Host backend and the runner that keeps the sandboxes of the chats live
// there, shared by the add-on and by the chats' built-in bash.
type (
	Backend      = coresandbox.Backend
	Discarder    = coresandbox.Discarder
	Env          = coresandbox.Env
	Spec         = coresandbox.Spec
	PortHandler  = coresandbox.PortHandler
	Sandbox      = coresandbox.Sandbox
	Cmd          = coresandbox.Cmd
	TTY          = coresandbox.TTY
	Process      = coresandbox.Process
	SnapshotRef  = coresandbox.SnapshotRef
	IndexedEntry = coresandbox.IndexedEntry
	IndexRecord  = coresandbox.IndexRecord
	UpperIndex   = coresandbox.UpperIndex
)

// Defaults for the zero fields of a Spec and a Cmd.
const (
	DefaultCPUs   = coresandbox.DefaultCPUs
	DefaultMemory = coresandbox.DefaultMemory
	DefaultDisk   = coresandbox.DefaultDisk
	DefaultDir    = coresandbox.DefaultDir
	AgentPort     = coresandbox.AgentPort
)

// CheckPorts reports a port outside 1 to 65535, the reserved AgentPort or a
// nil handler.
func CheckPorts(ports map[int]PortHandler) error { return coresandbox.CheckPorts(ports) }
