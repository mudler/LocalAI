// Package textindex keeps the text of the documents in the chats' workspace
// (PDF, Word, Excel, PowerPoint), so that a search finds a file by the words
// inside it without reading the file again. The text lives in LocalAI's own
// store, one entry per workspace node, never as a file in a user's folders.
//
// An entry is not a permission: whoever searches must check, for each entry,
// that they may read the node it belongs to and that the node still has the
// content the entry was made from (see files.Service). The Indexer keeps the
// entries in step with the workspace journal.
package textindex

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/extract"
)

// ErrNotFound means there is no entry for the node.
var ErrNotFound = errors.New("textindex: no entry")

// Status is where an entry is in its life.
type Status string

// Statuses of an entry.
const (
	// StatusPending: the file was written and its text is not read yet.
	StatusPending Status = "pending"
	// StatusReady: the text is stored.
	StatusReady Status = "ready"
	// StatusFailed: the reader failed (a broken file, a timeout); Reason says
	// why. The file is read again when it changes.
	StatusFailed Status = "failed"
	// StatusSkipped: there is nothing to read, such as a PDF with no text
	// layer or a file over the size limit; Reason says why.
	StatusSkipped Status = "skipped"
)

// Entry is the text of one node of the workspace.
type Entry struct {
	NodeID int64
	// Path is the workspace path of the node when it was last seen.
	Path string
	// Owner is the principal whose change set wrote the content.
	Owner string
	// Hash is the blob hash of the content the entry was made from.
	Hash string
	// Extractor is the version of the reader that made the entry.
	Extractor int
	Status    Status
	Reason    string
	// Kind is the kind of document (extract.KindPages, KindTable...).
	Kind string
	// Sections say where each page, sheet or slide starts in Text.
	Sections []extract.Section
	// Text is the text, empty unless the entry is ready. Lists of pending
	// entries leave it out.
	Text      string
	Truncated bool
	Updated   time.Time
}

// Query selects ready entries for Search.
type Query struct {
	// Prefix is a workspace path: "/" (or empty) for everything, a folder
	// for the nodes at or below it, or a file.
	Prefix string
	// Contains keeps only the entries whose text contains it, ignoring case.
	// The caller gives it in lower case (strings.ToLower). Empty keeps every
	// ready entry, for a caller that matches by itself (a regular
	// expression).
	Contains string
	// AfterID returns only entries with a greater node ID, to page through
	// the results in node ID order.
	AfterID int64
	// Limit is the most entries returned; 0 means 20.
	Limit int
}

// Store keeps the entries. Both implementations meet the contract of
// textindextest.Run.
type Store interface {
	// Put inserts or replaces the entry of e.NodeID.
	Put(ctx context.Context, e Entry) error
	// Get returns the entry of a node, with its text, or ErrNotFound.
	Get(ctx context.Context, nodeID int64) (Entry, error)
	// ReadyByHash returns a ready entry made from the content hash by that
	// extractor version, so the same content is not read twice.
	ReadyByHash(ctx context.Context, hash string, extractor int) (Entry, bool, error)
	// Delete removes the entries of the nodes. A missing one is no error.
	Delete(ctx context.Context, nodeIDs ...int64) error
	// Move changes the path of the entries at from to to, and of the entries
	// below the folder from to the same place below to.
	Move(ctx context.Context, from, to string) error
	// Pending returns pending entries at or below prefix, oldest change first,
	// without their text.
	Pending(ctx context.Context, prefix string, limit int) ([]Entry, error)
	// List returns the entries at or below prefix in node ID order, after
	// afterID, without their text.
	List(ctx context.Context, prefix string, afterID int64, limit int) ([]Entry, error)
	// Search returns the ready entries that q selects, with their text, in
	// node ID order. It may return an entry whose text holds q.Contains only
	// across a line break: the caller matches each line.
	Search(ctx context.Context, q Query) ([]Entry, error)
	// CountPending counts the pending entries at or below prefix.
	CountPending(ctx context.Context, prefix string) (int, error)
	// PutDocument keeps the whole text read from a content hash by that
	// extractor version, for a document whose entry keeps only part of it
	// (Truncated). It replaces an earlier one of the same hash.
	PutDocument(ctx context.Context, hash string, extractor int, text string) error
	// Document returns the whole text kept for the content hash by that
	// extractor version, and whether there is one.
	Document(ctx context.Context, hash string, extractor int) (string, bool, error)
	// PruneDocuments removes the whole texts whose hash no entry has, so the
	// text of a deleted or purged file does not stay behind.
	PruneDocuments(ctx context.Context) error
	// Cursor is the last journal revision the indexer applied, 0 at first.
	Cursor(ctx context.Context) (int64, error)
	// SetCursor records the last journal revision the indexer applied.
	SetCursor(ctx context.Context, rev int64) error
}

// Under reports whether the workspace path p is prefix or below it.
func Under(p, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	prefix = strings.TrimSuffix(prefix, "/")
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// moved returns the path of p after a move of from to to, and whether the
// move touches it.
func moved(p, from, to string) (string, bool) {
	from, to = strings.TrimSuffix(from, "/"), strings.TrimSuffix(to, "/")
	switch {
	case p == from:
		return to, true
	case strings.HasPrefix(p, from+"/"):
		return to + p[len(from):], true
	}
	return p, false
}

// The limits of Pending and Search, and of List, when the caller gives none.
// Both stores use them, so they answer the same.
const (
	defaultLimit     = 20
	defaultListLimit = 100
)

func limitOr(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}
