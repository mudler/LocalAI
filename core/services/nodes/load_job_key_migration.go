package nodes

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

const loadJobTable = "model_load_jobs"

// prepareLoadJobKeyMigration readies an existing job table for the key on
// (tracking_key, node_id). A row without a node gets the empty node, so the
// NOT NULL column that AutoMigrate then asks for, and the primary key that
// includes it, accept every row. A table that does not exist yet needs nothing.
func prepareLoadJobKeyMigration(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(loadJobTable) || !m.HasColumn(&ModelLoadJob{}, "node_id") {
		return nil
	}
	if db.Dialector.Name() == "postgres" {
		// Write only when the column can still hold NULL: a frontend whose
		// role does not own the table must still start once the migration ran.
		col, err := postgresNodeIDColumn(db)
		if err != nil {
			return err
		}
		if col.IsNullable != "YES" {
			return nil
		}
	}
	if err := db.Exec(`UPDATE model_load_jobs SET node_id = '' WHERE node_id IS NULL`).Error; err != nil {
		return fmt.Errorf("giving load jobs without a node the empty node: %w", err)
	}
	return nil
}

// migrateLoadJobKey moves the primary key of the job table from tracking_key
// to (tracking_key, node_id). AutoMigrate creates a new table with the new key
// but never changes the key of a table that exists. The migration is
// idempotent: a table that already has the new key is left alone.
//
// Existing rows keep their values. The old key made tracking_key unique, so no
// two rows can collide under the new one.
func migrateLoadJobKey(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "postgres":
		return migrateLoadJobKeyPostgres(db)
	case "sqlite":
		return migrateLoadJobKeySQLite(db)
	default:
		return nil
	}
}

func migrateLoadJobKeyPostgres(db *gorm.DB) error {
	// AutoMigrate makes node_id NOT NULL but does not add the default to a
	// column that exists. A row an older binary inserts without a node must
	// still get the empty node. Only an ALTER that is needed runs, so a
	// frontend whose role does not own the table starts once this is done.
	col, err := postgresNodeIDColumn(db)
	if err != nil {
		return err
	}
	if col.Default == nil || *col.Default == "" {
		if err := db.Exec(`ALTER TABLE model_load_jobs ALTER COLUMN node_id SET DEFAULT ''`).Error; err != nil {
			return fmt.Errorf("setting the default node of model_load_jobs: %w", err)
		}
	}
	var cols []string
	if err := db.Raw(`SELECT a.attname FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = 'model_load_jobs'::regclass AND i.indisprimary`).Scan(&cols).Error; err != nil {
		return fmt.Errorf("reading the primary key of model_load_jobs: %w", err)
	}
	if slices.Contains(cols, "node_id") {
		return nil
	}
	var name string
	if err := db.Raw(`SELECT conname FROM pg_constraint
		WHERE conrelid = 'model_load_jobs'::regclass AND contype = 'p'`).Scan(&name).Error; err != nil {
		return fmt.Errorf("reading the primary key constraint of model_load_jobs: %w", err)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if name != "" {
			if err := tx.Exec(`ALTER TABLE model_load_jobs DROP CONSTRAINT ` + quoteIdent(name)).Error; err != nil {
				return fmt.Errorf("dropping the old primary key of model_load_jobs: %w", err)
			}
		}
		if err := tx.Exec(`ALTER TABLE model_load_jobs ADD PRIMARY KEY (tracking_key, node_id)`).Error; err != nil {
			return fmt.Errorf("adding the primary key (tracking_key, node_id) to model_load_jobs: %w", err)
		}
		xlog.Info("Load jobs are now keyed by model and node")
		return nil
	})
}

// postgresColumn is what the migration needs to know about node_id.
type postgresColumn struct {
	IsNullable string  `gorm:"column:is_nullable"`
	Default    *string `gorm:"column:column_default"`
}

func postgresNodeIDColumn(db *gorm.DB) (postgresColumn, error) {
	var col postgresColumn
	err := db.Raw(`SELECT is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'model_load_jobs' AND column_name = 'node_id'`).Scan(&col).Error
	if err != nil {
		return col, fmt.Errorf("reading the node_id column of model_load_jobs: %w", err)
	}
	return col, nil
}

// sqliteColumn is one row of PRAGMA table_info.
type sqliteColumn struct {
	Name string `gorm:"column:name"`
	PK   int    `gorm:"column:pk"`
}

// migrateLoadJobKeySQLite rebuilds the table, because SQLite cannot change the
// primary key of a table in place.
func migrateLoadJobKeySQLite(db *gorm.DB) error {
	var cols []sqliteColumn
	if err := db.Raw(`PRAGMA table_info(model_load_jobs)`).Scan(&cols).Error; err != nil {
		return fmt.Errorf("reading the columns of model_load_jobs: %w", err)
	}
	for _, c := range cols {
		if c.Name == "node_id" && c.PK > 0 {
			return nil
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		const old = "model_load_jobs_before_node_key"
		if err := tx.Exec(`ALTER TABLE model_load_jobs RENAME TO ` + old).Error; err != nil {
			return fmt.Errorf("renaming model_load_jobs: %w", err)
		}
		// The indexes keep their names when the table is renamed; drop them
		// so that the new table can create its own.
		var indexes []string
		if err := tx.Raw(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL`, old).Scan(&indexes).Error; err != nil {
			return fmt.Errorf("listing the indexes of model_load_jobs: %w", err)
		}
		for _, idx := range indexes {
			if err := tx.Exec(`DROP INDEX ` + quoteIdent(idx)).Error; err != nil {
				return fmt.Errorf("dropping index %s: %w", idx, err)
			}
		}
		if err := tx.Migrator().CreateTable(&ModelLoadJob{}); err != nil {
			return fmt.Errorf("creating model_load_jobs keyed by model and node: %w", err)
		}
		var fresh []sqliteColumn
		if err := tx.Raw(`PRAGMA table_info(model_load_jobs)`).Scan(&fresh).Error; err != nil {
			return fmt.Errorf("reading the columns of the new model_load_jobs: %w", err)
		}
		var shared []string
		for _, c := range fresh {
			if slices.ContainsFunc(cols, func(o sqliteColumn) bool { return o.Name == c.Name }) {
				shared = append(shared, quoteIdent(c.Name))
			}
		}
		list := strings.Join(shared, ", ")
		if err := tx.Exec(`INSERT INTO model_load_jobs (` + list + `) SELECT ` + list + ` FROM ` + old).Error; err != nil {
			return fmt.Errorf("copying load jobs: %w", err)
		}
		if err := tx.Exec(`DROP TABLE ` + old).Error; err != nil {
			return fmt.Errorf("dropping the old load job table: %w", err)
		}
		return nil
	})
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
