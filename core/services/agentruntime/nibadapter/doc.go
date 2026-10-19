// SPDX-License-Identifier: MIT

// Package nibadapter provides a bounded contract for github.com/mudler/nib
// v0.12.1. Evidence below refers to that exact module-cache release, not a
// modern checkout. Module sum: h1:7yKZeOWcIac62UoNb3J2R/oEtFYzebFwgap9HPjIJdI=.
//
// Source/API evidence:
//   - chat/session.go:513: NewSession(context.Context, types.Config,
//     chat.Callbacks, ...mcp.Transport) (*chat.Session, error).
//   - chat/session.go:1122,1134: ExportHistory and ExportContext return
//     []openai.ChatCompletionMessage. Context excludes regenerated system
//     messages; display history is not the protocol context.
//   - chat/session.go:1149,1164: ExportArtifacts returns []types.Artifact;
//     ExportPruning returns types.PruningState. Each export has independent
//     synchronization. There is no atomic four-part snapshot API.
//   - types/config.go:337-361 and chat/session.go:678-693: InitialHistory,
//     InitialContext, InitialArtifacts, InitialPruning restore those native
//     types. Pruning is restored only when InitialContext is nonempty.
//   - types/artifact.go:9 and types/pruning.go:8: artifact IDs are int64;
//     pruning maps are keyed by tool_call_id and include compression state.
//     mcp/artifacts.go:72-101 sorts snapshots by ID, skips nonpositive IDs
//     on restore, and overwrites duplicates. Admission rejects nonpositive
//     and duplicate IDs; artifact ordering is not identity.
//     Admission also rejects system messages in context and history: native
//     exports omit regenerated context systems and promise system-free history.
//   - chat/callbacks.go:43-57,163-247: StreamEvent and Callbacks expose
//     streaming, text/reasoning/status/errors, optional approval/questions,
//     tool lifecycle, helper lifecycle/title, compaction/pruning, park/resume.
//     NewCallbacks compiles against those exact callback signatures; contract
//     tests invoke them directly. Streaming support here proves projection,
//     not provider streaming, usage accounting, or a secure execution loop.
//
// Executable evidence is TestNibAdapter: two real isolated sessions restore
// and export the native checkpoint; protocol tool IDs and artifact IDs survive
// separately from history; callback tests exercise projection, concurrent late
// identity, rejection, missing-policy failure, and question ownership. Every
// advertised true field is limited to that tested surface. Subtasks, GoalHooks
// and LoopHooks remain false: this package does not prove their authorized
// execution semantics. HelperEvents means observation, not subtask admission.
//
// Security blocker: chat/session.go:798-883 implements an optional prompt, not
// a mandatory host policy hook. Auto/turn approval, allowed tools, read-only
// rules and classifier approval bypass OnToolCall; the nil callback path at
// 868-869 approves. HookConfig (types/config.go:193-201) is a shell-command
// lifecycle mechanism, not a safe host Go policy callback. RequireSecureTools
// therefore rejects even a supplied policy with ErrMandatoryPolicyUnsupported.
// Task5/7 tool-enabled integration requires independently reviewed upstream
// repair. Direct callback denial tests do not claim upstream interception.
//
// Checkpoints require a host-attested quiescent boundary, including helpers.
// The adapter does not implement a session loop or atomic exports. These raw
// payloads provide no restricted add-on retention or authorization guarantee;
// Task6 must enforce those separately. Do not flatten LocalAI's rich visible
// transcript into nib display history. No transport or powerful tool is
// started by this adapter. Tests use temporary HOME/state/base directories,
// disable LSP autodetection, and never send model turns or external requests.
package nibadapter
