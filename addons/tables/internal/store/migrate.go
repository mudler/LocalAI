// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrIncompatibleFormat means the data on disk is in a format this executable
// cannot read or cannot reach by migration. The data is left as it was.
var ErrIncompatibleFormat = errors.New("data format is not supported by this version")

// Migration states written to the status file.
const (
	MigrationRunning  = "running"
	MigrationComplete = "complete"
	MigrationFailed   = "failed"
)

// MigrationStatus is what the host reads to learn whether a start migrated
// the data. It is written to a file because the add-on has no network access
// of its own before it is ready.
type MigrationStatus struct {
	State     string `json:"state"`
	From      int    `json:"from"`
	To        int    `json:"to"`
	Databases int    `json:"databases"`
	Migrated  int    `json:"migrated"`
	Error     string `json:"error,omitempty"`
	Updated   string `json:"updated"`
}

// steps maps a target format version to the change of one database from the
// version before it. A step runs inside the transaction of its database, so a
// failing step leaves that database as it was. No format after the first
// exists yet, so the table is empty; tests register their own.
// formatVersion is the format this build writes; tests raise it.
var formatVersion = FormatVersion

var steps = map[int]func(ctx context.Context, tx *sql.Tx) error{}

const storeMetaSchema = `CREATE TABLE IF NOT EXISTS store_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`

func (s *Store) databaseIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dbDir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, tmpPrefix) || !strings.HasSuffix(n, ".db") {
			continue
		}
		if id := strings.TrimSuffix(n, ".db"); idPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func formatOf(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var v string
	err := q.QueryRowContext(ctx, "SELECT value FROM tables_meta WHERE key='format'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, ErrIncompatibleFormat
	}
	return n, nil
}

// StoredFormat is the format version of the data on disk: the version the
// ledger records, or for data older than the record, the smallest version of
// its databases. An empty store has the current one.
func (s *Store) StoredFormat(ctx context.Context) (int, error) {
	if _, err := s.ledger.ExecContext(ctx, storeMetaSchema); err != nil {
		return 0, err
	}
	var v string
	err := s.ledger.QueryRowContext(ctx, "SELECT value FROM store_meta WHERE key='format'").Scan(&v)
	if err == nil {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 {
			return 0, ErrIncompatibleFormat
		}
		return n, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	ids, err := s.databaseIDs()
	if err != nil {
		return 0, err
	}
	lowest := formatVersion
	for _, id := range ids {
		db, err := s.openDatabase(id)
		if err != nil {
			return 0, err
		}
		n, err := formatOf(ctx, db)
		_ = db.Close()
		if err != nil {
			return 0, err
		}
		if n < lowest {
			lowest = n
		}
	}
	return lowest, nil
}

func (s *Store) setStoredFormat(ctx context.Context, v int) error {
	if _, err := s.ledger.ExecContext(ctx, storeMetaSchema); err != nil {
		return err
	}
	_, err := s.ledger.ExecContext(ctx, "INSERT INTO store_meta(key,value) VALUES('format',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.Itoa(v))
	return err
}

// Migrate moves the data from format fromVersion to toVersion. The caller
// owns the data directory exclusively: the host stops every other process
// before it starts the one that migrates. Each database changes in one
// transaction, the version of the whole store is recorded last, and a run
// that stopped half way is resumed by the next call (a database already at the
// target version is skipped). A downgrade, a version this executable has no
// path to and data in another format than fromVersion are refused with
// ErrIncompatibleFormat before anything is written.
func (s *Store) Migrate(ctx context.Context, fromVersion, toVersion int) error {
	return s.migrate(ctx, fromVersion, toVersion, nil)
}

func (s *Store) migrate(ctx context.Context, fromVersion, toVersion int, report func(MigrationStatus)) error {
	if fromVersion < 1 || toVersion < fromVersion || toVersion > formatVersion {
		return ErrIncompatibleFormat
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.StoredFormat(ctx)
	if err != nil {
		return err
	}
	if stored == toVersion {
		return nil
	}
	if stored != fromVersion {
		return ErrIncompatibleFormat
	}
	for v := fromVersion + 1; v <= toVersion; v++ {
		if steps[v] == nil {
			return fmt.Errorf("%w: no step to version %d", ErrIncompatibleFormat, v)
		}
	}
	ids, err := s.databaseIDs()
	if err != nil {
		return err
	}
	st := MigrationStatus{State: MigrationRunning, From: fromVersion, To: toVersion, Databases: len(ids)}
	emit := func() {
		st.Updated = time.Now().UTC().Format(time.RFC3339Nano)
		if report != nil {
			report(st)
		}
	}
	fail := func(err error) error {
		st.State, st.Error = MigrationFailed, err.Error()
		emit()
		return err
	}
	emit()
	for v := fromVersion + 1; v <= toVersion; v++ {
		for _, id := range ids {
			if err := s.migrateDatabase(ctx, id, v); err != nil {
				return fail(fmt.Errorf("database %s to format %d: %w", id, v, err))
			}
			if v == toVersion {
				st.Migrated++
			}
		}
		// The store version moves one step at a time, so a stop between two
		// steps resumes from the right place.
		if err := s.setStoredFormat(ctx, v); err != nil {
			return fail(err)
		}
		emit()
	}
	st.State = MigrationComplete
	emit()
	return nil
}

func (s *Store) migrateDatabase(ctx context.Context, id string, v int) error {
	db, err := s.openDatabase(id)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	cur, err := formatOf(ctx, tx)
	if err == nil && cur >= v {
		return tx.Rollback()
	}
	if err == nil && cur != v-1 {
		err = ErrIncompatibleFormat
	}
	if err == nil {
		err = steps[v](ctx, tx)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "INSERT INTO tables_meta(key,value) VALUES('format',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.Itoa(v))
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Upgrade brings the data to the format of this executable and records the
// progress in the status file at statusPath (when not empty), so the host
// can tell a start that migrated from one that could not. Data in a newer
// format than this executable reads is refused and left as it is.
func (s *Store) Upgrade(ctx context.Context, statusPath string) error {
	write := func(st MigrationStatus) {
		if statusPath != "" {
			_ = writeStatus(statusPath, st)
		}
	}
	stored, err := s.StoredFormat(ctx)
	if err != nil {
		write(MigrationStatus{State: MigrationFailed, Error: err.Error(), Updated: time.Now().UTC().Format(time.RFC3339Nano)})
		return err
	}
	if stored > formatVersion {
		write(MigrationStatus{State: MigrationFailed, From: stored, To: formatVersion, Error: ErrIncompatibleFormat.Error(), Updated: time.Now().UTC().Format(time.RFC3339Nano)})
		return ErrIncompatibleFormat
	}
	if stored == formatVersion {
		write(MigrationStatus{State: MigrationComplete, From: stored, To: stored, Updated: time.Now().UTC().Format(time.RFC3339Nano)})
		return nil
	}
	return s.migrate(ctx, stored, formatVersion, write)
}

func writeStatus(path string, st MigrationStatus) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".migration-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
