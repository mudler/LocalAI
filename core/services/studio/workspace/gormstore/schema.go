package gormstore

import (
	"context"
	"fmt"
)

const schemaVersion = 1

// ddl is the table definitions, in order. %[1]s is the integer primary key of
// the dialect. The names carry a prefix because the tables share a database
// with the rest of LocalAI.
func ddl(d dialect) []string {
	id := d.idColumn
	return []string{
		`CREATE TABLE IF NOT EXISTS studio_ws_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS studio_ws_nodes (
			id        ` + id + `,
			parent_id BIGINT REFERENCES studio_ws_nodes(id),
			name      TEXT NOT NULL,
			name_fold TEXT NOT NULL,
			kind      TEXT NOT NULL CHECK (kind IN ('file', 'dir')),
			blob      TEXT NOT NULL DEFAULT '',
			size      BIGINT NOT NULL DEFAULT 0,
			version   BIGINT NOT NULL,
			rev       BIGINT NOT NULL,
			deleted   INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS studio_ws_nodes_live_name ON studio_ws_nodes (parent_id, name_fold) WHERE deleted = 0`,
		`CREATE INDEX IF NOT EXISTS studio_ws_nodes_parent ON studio_ws_nodes (parent_id)`,
		`CREATE TABLE IF NOT EXISTS studio_ws_changesets (
			rev        BIGINT PRIMARY KEY,
			actor      TEXT NOT NULL,
			chat_id    TEXT NOT NULL DEFAULT '',
			title      TEXT NOT NULL,
			kind       TEXT NOT NULL,
			undoes     BIGINT NOT NULL DEFAULT 0,
			created_at BIGINT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS studio_ws_journal (
			id          ` + id + `,
			rev         BIGINT NOT NULL REFERENCES studio_ws_changesets(rev),
			node_id     BIGINT NOT NULL REFERENCES studio_ws_nodes(id),
			op          TEXT NOT NULL,
			old_parent  BIGINT,
			old_name    TEXT NOT NULL DEFAULT '',
			old_path    TEXT NOT NULL DEFAULT '',
			old_blob    TEXT NOT NULL DEFAULT '',
			old_size    BIGINT NOT NULL DEFAULT 0,
			old_deleted INTEGER NOT NULL DEFAULT 1,
			new_parent  BIGINT,
			new_name    TEXT NOT NULL DEFAULT '',
			new_path    TEXT NOT NULL DEFAULT '',
			new_blob    TEXT NOT NULL DEFAULT '',
			new_size    BIGINT NOT NULL DEFAULT 0,
			new_deleted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS studio_ws_journal_rev ON studio_ws_journal (rev)`,
		// The files of a chat are read from the change sets that chat made.
		`CREATE INDEX IF NOT EXISTS studio_ws_changesets_chat ON studio_ws_changesets (chat_id)`,
		`CREATE INDEX IF NOT EXISTS studio_ws_journal_node ON studio_ws_journal (node_id)`,
		`CREATE INDEX IF NOT EXISTS studio_ws_journal_old_path ON studio_ws_journal (old_path)`,
		`CREATE INDEX IF NOT EXISTS studio_ws_journal_new_path ON studio_ws_journal (new_path)`,
		`CREATE INDEX IF NOT EXISTS studio_ws_journal_new_parent ON studio_ws_journal (new_parent, new_name)`,
		`CREATE TABLE IF NOT EXISTS studio_ws_grants (
			principal TEXT NOT NULL,
			node_id   BIGINT NOT NULL REFERENCES studio_ws_nodes(id),
			mode      INTEGER NOT NULL,
			PRIMARY KEY (principal, node_id)
		)`,
	}
}

// migrate creates the tables and the root folder. It is safe to run on every
// start and from several frontends at once.
func migrate(ctx context.Context, c *conn) error {
	t, err := c.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = t.Rollback() }()
	for _, stmt := range ddl(c.d) {
		if _, err := t.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("workspace: create tables: %w", err)
		}
	}
	// The root has the fixed id 1, so it is inserted with that id.
	if _, err := t.ExecContext(ctx, `INSERT INTO studio_ws_nodes (id, parent_id, name, name_fold, kind, version, rev)
		VALUES (1, NULL, '', '', 'dir', 1, 0) ON CONFLICT (id) DO NOTHING`); err != nil {
		return err
	}
	if _, err := t.ExecContext(ctx, `INSERT INTO studio_ws_meta (key, value) VALUES ('rev', '0') ON CONFLICT (key) DO NOTHING`); err != nil {
		return err
	}
	if _, err := t.ExecContext(ctx, `INSERT INTO studio_ws_meta (key, value) VALUES ('schema', ?) ON CONFLICT (key) DO NOTHING`, fmt.Sprint(schemaVersion)); err != nil {
		return err
	}
	if c.d.name == "postgres" {
		// The root was inserted with an explicit id: move the sequence past it.
		if _, err := t.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('studio_ws_nodes', 'id'), GREATEST((SELECT MAX(id) FROM studio_ws_nodes), 1))`); err != nil {
			return err
		}
	}
	return t.Commit()
}
