// Package sandbox defines how an agent harness runs shell commands in an
// isolated guest that sees a workspace view as files.
//
// A Backend creates a Sandbox from a Spec. The guest sees the view at
// /workspace as an overlay: the read-only lower tree that
// workspace.Store.Materialize builds, and a per-sandbox upper layer that
// holds what the guest changes. Checkpoint commits the upper layer as one
// change set with workspace.Store.CommitUpper. The base revision stays the
// one that Materialize returned; the sandbox passes its own earlier
// revisions in workspace.UpperOptions.Own, so its own changes are not
// conflicts. Refresh gives the guest current content and restarts it.
//
// The guest has no network device. A host PortHandler serves the
// connections that the guest opens to 127.0.0.1 on one port.
//
// UpperIndex is a helper for backends: it makes repeated checkpoints of one
// upper layer correct (skip unchanged files, send deletes of files that an
// earlier checkpoint committed).
//
// Every backend must pass sbtest.RunBackendSuite.
package sandbox
