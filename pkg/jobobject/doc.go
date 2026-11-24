// Package jobobject ties a process that LocalAI did not start through
// go-processmanager to a Windows job object, so the process and everything it
// starts end with LocalAI. Backends started through go-processmanager use its
// WithJobObject option instead, which also creates the process suspended.
//
// The package has code on Windows only.
package jobobject
