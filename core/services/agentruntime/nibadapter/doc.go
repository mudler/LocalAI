// SPDX-License-Identifier: MIT

// Package nibadapter provides a bounded contract for github.com/mudler/nib
// v0.12.2-0.20261020064257-4bdb29c37b09, with Cogito selected at
// v0.11.1-0.20261002063524-935bd35a9b27. The nib module sum is
// h1:ZVNhqCw/3gN02yf1mC6dqUf1LAw+ZCYIGxLJk6qQc8U=.
//
// Source/API evidence in that exact module:
//   - chat/session.go: NewSession accepts context, types.Config, Callbacks and
//     optional MCP transports. ExportHistory and ExportContext return native
//     OpenAI messages. Context excludes regenerated system messages; display
//     history is not the protocol context.
//   - ExportArtifacts and ExportPruning return types.Artifact and PruningState
//     snapshots with independent synchronization, not an atomic checkpoint.
//     InitialHistory, InitialContext, InitialArtifacts and InitialPruning in
//     types/config.go restore those native types. Pruning restoration requires
//     nonempty InitialContext. The compatible checkpoint wire version remains
//     nib-v0.12.1/checkpoint-1.
//   - types/artifact.go, types/pruning.go and mcp/artifacts.go: artifact IDs are
//     int64; pruning maps use tool_call_id and retain compression state.
//     Admission rejects nonpositive/duplicate artifact IDs and system messages
//     rather than silently losing state during native restoration.
//   - chat/callbacks.go and chat/session.go: ToolPolicy is a mandatory Go veto
//     before hooks and ordinary approval, including auto, turn, tool, prefix,
//     read-only and classifier fast paths. A successful policy does not grant
//     approval. OnToolCall remains a separate optional human prompt.
//     NewCallbacks requires a nonnil host policy, captures immutable host
//     identity and clones ExternalSources before invoking the policy. Its
//     default ordinary approval callback denies. RequireSecureTools(nil) fails.
//
// TestNibAdapter restores and exports native checkpoints, validates callback
// projection and immutable identity, and drives real nib/Cogito/MCP dispatch
// using local fake model endpoints and counter-only tools. Paired root controls
// prove execution versus denial under public approval fast paths; prompt denial
// proves policy success alone is insufficient. A spawned helper inherits a fake
// MCP tool: the host policy receives its registered agent ID and intact call
// arguments, and a veto suppresses the actual body despite an inherited turn
// grant. These are interception tests, not authorized subtask admission.
// Subtasks, GoalHooks and LoopHooks remain false. HelperEvents means observation
// only. Streaming proves callback projection, not provider streaming or usage
// accounting; JSON dispatch fixtures explicitly disable OnStream.
//
// Checkpoints require a host-attested quiescent boundary, including helpers.
// This adapter implements neither a session loop nor atomic exports, lease
// fencing, helper authorization, private retention or key lifecycle. Raw native
// payloads are not an authorized visible transcript. Do not flatten LocalAI's
// rich transcript into nib display history. Production transport/tool startup
// remains the host's responsibility. Tests isolate HOME/state/base directories,
// disable LSP autodetection and execute only fake local counter bodies; no shell,
// browser, plugin or external network tool runs.
package nibadapter
