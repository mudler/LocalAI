// Package llamacppsettings maps llama-server command-line flags to LocalAI
// model config keys and back.
//
// The table in flags.go is the single source of truth. ParseCommand turns a
// pasted "llama-server ..." command line into model config fields and
// options. BuildCommand does the inverse, for a live preview. Neither
// function runs anything: the command line is only tokenised, never handed
// to a shell.
//
// placement.go builds and recognises the "-ot" tensor override patterns that
// keep MoE expert tensors on the CPU. Nothing in this package applies a
// placement by default; it is always an explicit, opt-in setting.
package llamacppsettings
