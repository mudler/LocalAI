// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/tables/internal/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// metaOf reads one tables_meta value straight from the database file.
func metaOf(dir, id, key string) (string, bool) {
	GinkgoHelper()
	db, e := sql.Open("sqlite3", filepath.Join(dir, "databases", id+".db"))
	Expect(e).NotTo(HaveOccurred())
	defer func() { _ = db.Close() }()
	var v string
	e = db.QueryRow("SELECT value FROM tables_meta WHERE key=?", key).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false
	}
	Expect(e).NotTo(HaveOccurred())
	return v, true
}

func readStatus(path string) store.MigrationStatus {
	GinkgoHelper()
	b, e := os.ReadFile(path)
	Expect(e).NotTo(HaveOccurred())
	var st store.MigrationStatus
	Expect(json.Unmarshal(b, &st)).To(Succeed())
	return st
}

var _ = Describe("Format migration", func() {
	twoDatabases := func() (string, *store.Store) {
		GinkgoHelper()
		dir := filepath.Join(GinkgoT().TempDir(), "tables")
		s := open(dir, store.Options{})
		for i, id := range []string{idA, idB} {
			_, e := s.Create(context.Background(), id, "Db "+id[:2], "op-create-"+id[:2])
			Expect(e).NotTo(HaveOccurred(), "database %d", i)
		}
		return dir, s
	}

	It("records the current format of a new store and migrates nothing", func() {
		_, s := twoDatabases()
		n, e := s.StoredFormat(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(n).To(Equal(store.FormatVersion))
		status := filepath.Join(GinkgoT().TempDir(), "migration.json")
		Expect(s.Upgrade(context.Background(), status)).To(Succeed())
		Expect(readStatus(status).State).To(Equal(store.MigrationComplete))
		Expect(s.Migrate(context.Background(), 1, 1)).To(Succeed())
	})

	It("migrates every database in its own transaction, records the version last and repeats as a no-op", func() {
		dir, s := twoDatabases()
		undo := store.SetMigrationStep(2, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "INSERT INTO tables_meta(key,value) VALUES('migrated','yes')")
			return e
		})
		DeferCleanup(undo)
		store.SetCurrentFormat(2)
		status := filepath.Join(GinkgoT().TempDir(), "migration.json")
		Expect(s.Upgrade(context.Background(), status)).To(Succeed())
		for _, id := range []string{idA, idB} {
			v, ok := metaOf(dir, id, "format")
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("2"))
			m, ok := metaOf(dir, id, "migrated")
			Expect(ok).To(BeTrue())
			Expect(m).To(Equal("yes"))
		}
		n, e := s.StoredFormat(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(n).To(Equal(2))
		st := readStatus(status)
		Expect(st.State).To(Equal(store.MigrationComplete))
		Expect(st.From).To(Equal(1))
		Expect(st.To).To(Equal(2))
		Expect(st.Migrated).To(Equal(2))
		// A second run changes nothing and does not run the step again.
		runs := 0
		store.SetMigrationStep(2, func(context.Context, *sql.Tx) error { runs++; return nil })
		Expect(s.Migrate(context.Background(), 1, 2)).To(Succeed())
		Expect(runs).To(BeZero())
	})

	It("leaves a database whose step fails exactly as it was and resumes on the next run", func() {
		dir, s := twoDatabases()
		calls := 0
		undo := store.SetMigrationStep(2, func(ctx context.Context, tx *sql.Tx) error {
			calls++
			if _, e := tx.ExecContext(ctx, "INSERT INTO tables_meta(key,value) VALUES('migrated','yes')"); e != nil {
				return e
			}
			if calls == 2 {
				return errors.New("step failed after a write")
			}
			return nil
		})
		DeferCleanup(undo)
		store.SetCurrentFormat(2)
		status := filepath.Join(GinkgoT().TempDir(), "migration.json")
		e := s.Upgrade(context.Background(), status)
		Expect(e).To(HaveOccurred())
		Expect(readStatus(status).State).To(Equal(store.MigrationFailed))
		Expect(readStatus(status).Error).To(ContainSubstring("step failed"))
		// The first database finished, the second is untouched, and the store
		// still says it is in the old format.
		v, _ := metaOf(dir, idA, "format")
		Expect(v).To(Equal("2"))
		v, _ = metaOf(dir, idB, "format")
		Expect(v).To(Equal("1"))
		_, wrote := metaOf(dir, idB, "migrated")
		Expect(wrote).To(BeFalse())
		n, e := s.StoredFormat(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		// Resumed: the finished database is skipped and the other completes.
		Expect(s.Upgrade(context.Background(), status)).To(Succeed())
		Expect(calls).To(Equal(3))
		v, _ = metaOf(dir, idB, "format")
		Expect(v).To(Equal("2"))
		n, _ = s.StoredFormat(context.Background())
		Expect(n).To(Equal(2))
	})

	It("refuses data in a newer format, a downgrade and a version it has no path to, and writes nothing", func() {
		dir, s := twoDatabases()
		undo := store.SetMigrationStep(2, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "INSERT INTO tables_meta(key,value) VALUES('migrated','yes')")
			return e
		})
		store.SetCurrentFormat(2)
		Expect(s.Upgrade(context.Background(), "")).To(Succeed())
		undo()
		// This build only reads format 1 again.
		status := filepath.Join(GinkgoT().TempDir(), "migration.json")
		e := s.Upgrade(context.Background(), status)
		Expect(e).To(MatchError(store.ErrIncompatibleFormat))
		Expect(readStatus(status).State).To(Equal(store.MigrationFailed))
		Expect(s.Migrate(context.Background(), 2, 1)).To(MatchError(store.ErrIncompatibleFormat))
		Expect(s.Migrate(context.Background(), 1, 3)).To(MatchError(store.ErrIncompatibleFormat))
		v, _ := metaOf(dir, idA, "format")
		Expect(v).To(Equal("2"))
	})
})
