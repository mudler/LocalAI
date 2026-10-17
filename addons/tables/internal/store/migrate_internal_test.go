// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"database/sql"
)

// SetMigrationStep registers a format step for a test and returns the undo.
// It exists only in test builds.
func SetMigrationStep(v int, fn func(context.Context, *sql.Tx) error) func() {
	prev, had := steps[v]
	steps[v] = fn
	old := formatVersion
	return func() {
		if had {
			steps[v] = prev
		} else {
			delete(steps, v)
		}
		formatVersion = old
	}
}

// SetCurrentFormat raises the format this build writes, for a test.
func SetCurrentFormat(v int) { formatVersion = v }
