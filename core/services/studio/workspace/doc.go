// Package workspace is a versioned file store for users and agents.
//
// The store keeps each file version as an immutable blob named by its
// SHA-256, and a tree of nodes that point to blobs. Every change is an
// operation in an append-only journal. Operations are grouped into change
// sets, one per revision, and a change set is the unit of undo. Nothing is
// hard-deleted: a delete sets a tombstone.
//
// Principals are opaque IDs that the host application gives; the store never
// authenticates. A grant gives a principal read or write access to a folder
// and everything below it. A call without access returns ErrNotFound, so a
// principal cannot learn which paths exist.
//
// For a sandbox, Materialize builds a read-only tree of hard links to the
// blobs of a view, and CommitUpper commits an overlay upper layer, read
// through an UpperSource, as one change set.
//
// Storage layout under the root folder:
//
//	workspace.db           SQLite: nodes, journal, change sets, grants, meta
//	blobs/ab/cd/<sha256>   blob files, mode 0444
//	tmp/                   uploads in progress
//
// Open one LocalStore per root folder in a process.
package workspace
