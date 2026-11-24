// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrStale means the database moved past the revision a change was previewed
// against. Nothing was applied. Use errors.As with *StaleError for the current
// revision.
var ErrStale = errors.New("database changed since the preview")

// StaleError carries the revision the database has now.
type StaleError struct {
	Current contract.Revision
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("%v: now schema %d, data %d", ErrStale, e.Current.Schema, e.Current.Data)
}
func (e *StaleError) Is(target error) bool { return target == ErrStale }

// Failpoints name the places a test may stop an operation to model a crash.
// They are never set in production.
const (
	FailBeforeCommit = "before-commit"
	FailAfterCommit  = "after-commit"
	FailRebuildCopy  = "rebuild-copy"
)

// SetFailpoint installs a hook that is called with a failpoint name. A non-nil
// result aborts the operation at that point as if the process had died there.
// It exists for the crash tests of this package and the executable's suites.
func (s *Store) SetFailpoint(f func(point string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failpoint = f
}

func (s *Store) fail(point string) error {
	if s.failpoint == nil {
		return nil
	}
	return s.failpoint(point)
}

// bumpSchema moves the schema revision by one inside tx and returns the new
// revision pair. The revision and the change commit together or not at all.
func bumpSchema(ctx context.Context, tx *sql.Tx, cur contract.Revision) (contract.Revision, error) {
	next := contract.Revision{Schema: cur.Schema + 1, Data: cur.Data}
	_, err := tx.ExecContext(ctx, "UPDATE tables_meta SET value=? WHERE key='schema_revision'", strconv.FormatUint(next.Schema, 10))
	return next, err
}

// bumpData moves the data revision by one inside tx. Row writes and their
// receipt commit with it or not at all.
func bumpData(ctx context.Context, tx *sql.Tx, cur contract.Revision) (contract.Revision, error) {
	next := contract.Revision{Schema: cur.Schema, Data: cur.Data + 1}
	_, err := tx.ExecContext(ctx, "UPDATE tables_meta SET value=? WHERE key='data_revision'", strconv.FormatUint(next.Data, 10))
	return next, err
}
