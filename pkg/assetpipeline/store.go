// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrConflict = errors.New("pipeline revision conflict")
	ErrNotFound = errors.New("pipeline not found")
	ErrInvalid  = errors.New("invalid pipeline command")
)

// Store is opened only by the add-on owner. Frontends never maintain copies.
// SQLite commits the state and idempotency receipt together, so a response lost
// after commit can be retried without repeating a transition.
type Store struct {
	db       *sql.DB
	dir      string
	uploadMu sync.Mutex
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", filepath.Join(dir, "pipelines.sqlite")+"?_busy_timeout=5000&_journal_mode=WAL&_synchronous=FULL&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS pipelines (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, body BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS receipts (resource TEXT NOT NULL, operation TEXT NOT NULL, digest TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(resource, operation));`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_events (
resource TEXT NOT NULL, branch TEXT NOT NULL, position INTEGER NOT NULL,
id TEXT NOT NULL, body BLOB NOT NULL,
PRIMARY KEY(resource, branch, position), UNIQUE(resource, id));`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS artifacts (resource TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, mime TEXT NOT NULL, bytes INTEGER NOT NULL, sha TEXT NOT NULL, PRIMARY KEY(resource, id))`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS uploads (resource TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, mime TEXT NOT NULL, body BLOB NOT NULL, result TEXT NOT NULL DEFAULT '', PRIMARY KEY(resource,id))`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := os.MkdirAll(filepath.Join(dir, "artifacts"), 0700); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db, dir: dir}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Read(ctx context.Context, id string) (Workspace, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT body FROM pipelines WHERE id = ?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, err
	}

	w, err := readJournal(ctx, s.db, id, raw)
	if err != nil {
		return Workspace{}, fmt.Errorf("invalid saved pipeline: %w", err)
	}

	return w, nil
}

// Update's command must include all mutation arguments. An operation ID cannot
// be reused for a different command, even with the same expected revision.
// expected=0 creates a new resource; all subsequent updates require its revision.
func (s *Store) Update(ctx context.Context, id, operation string, expected uint64, command any, change func(*Workspace) error) (Workspace, error) {
	if id == "" || operation == "" {
		return Workspace{}, errors.New("resource and operation IDs are required")
	}

	encoded, err := json.Marshal(struct {
		Revision uint64
		Command  any
	}{expected, command})
	if err != nil {
		return Workspace{}, err
	}

	hash := sha256.Sum256(encoded)
	digest := hex.EncodeToString(hash[:])

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, err
	}

	defer func() { _ = tx.Rollback() }()

	var (
		savedDigest string
		raw         []byte
	)
	err = tx.QueryRowContext(ctx, "SELECT digest, body FROM receipts WHERE resource = ? AND operation = ?", id, operation).Scan(&savedDigest, &raw)
	if err == nil {
		if savedDigest != digest {
			return Workspace{}, ErrConflict
		}

		return readJournal(ctx, tx, id, raw)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, err
	}

	var revision uint64
	err = tx.QueryRowContext(ctx, "SELECT revision, body FROM pipelines WHERE id = ?", id).Scan(&revision, &raw)
	var w Workspace
	if errors.Is(err, sql.ErrNoRows) {
		if expected != 0 {
			return w, ErrNotFound
		}
	} else if err != nil {
		return w, err
	} else {
		if expected == 0 || revision != expected {
			return w, ErrConflict
		}

		w, err = readJournal(ctx, tx, id, raw)
		if err != nil {
			return w, err
		}
	}

	lineage := branchLineage(w)
	if err := change(&w); err != nil {
		return Workspace{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	updatedLineage := branchLineage(w)
	for branch, original := range lineage {
		if updatedLineage[branch] != original {
			return Workspace{}, errors.New("cannot rewrite branch ancestry")
		}
	}
	if w.Revision <= expected {
		w.Revision = expected + 1
	}

	raw, err = appendJournal(ctx, tx, id, w)
	if err != nil {
		return Workspace{}, err
	}

	if _, err = tx.ExecContext(ctx, "INSERT INTO pipelines(id, revision, body) VALUES(?, ?, ?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision, body=excluded.body", id, w.Revision, raw); err != nil {
		return Workspace{}, err
	}

	if _, err = tx.ExecContext(ctx, "INSERT INTO receipts(resource, operation, digest, body) VALUES(?, ?, ?, ?)", id, operation, digest, raw); err != nil {
		return Workspace{}, err
	}
	// Old operations still carry stale revisions after their receipts expire;
	// they conflict rather than executing again.
	if _, err = tx.ExecContext(ctx, "DELETE FROM receipts WHERE resource = ? AND rowid NOT IN (SELECT rowid FROM receipts WHERE resource = ? ORDER BY rowid DESC LIMIT 32)", id, id); err != nil {
		return Workspace{}, err
	}

	if err = tx.Commit(); err != nil {
		return Workspace{}, err
	}

	return w, nil
}

func (s *Store) IDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM pipelines")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, rows.Err()
}
