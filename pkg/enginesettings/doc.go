// Package enginesettings maps the runtime settings of an inference engine to
// LocalAI model config keys, so the model editor and the assistant can show,
// check and translate them from one table per engine.
//
// An Engine describes one engine: its id, the backend names it serves, its
// flag table, the groups and presets the editor draws, and optional
// capabilities. With the command-line capability, ParseCommand turns a pasted
// command line into model config keys and options, and BuildCommand writes the
// command of a config for a preview. Neither runs anything: the command line is
// only tokenised, never handed to a shell. With the tensor-placement
// capability, the engine builds and recognises an options entry that keeps MoE
// expert tensors on the CPU. A placement is always opt-in.
//
// The engines are collected in a Registry. The package itself knows no engine:
// each engine lives in its own sub-package (for example llamacpp), and the
// builtin package registers the ones LocalAI ships.
package enginesettings
