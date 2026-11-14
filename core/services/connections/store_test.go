package connections_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/connections"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func rec(id, name string) connections.Record {
	return connections.Record{ID: id, Name: name, Kind: connections.KindLocalAI, URL: "https://remote.example.com", Status: connections.StatusNever, Created: time.Now().UTC()}
}

// storeContract is what every connections.Store must do.
func storeContract(newStore func() connections.Store) {
	var s connections.Store
	ctx := context.Background()
	BeforeEach(func() { s = newStore() })

	It("creates, gets and lists records by name", func() {
		Expect(s.Create(ctx, rec("b", "Beta"))).To(Succeed())
		Expect(s.Create(ctx, rec("a", "alpha"))).To(Succeed())
		got, err := s.Get(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("alpha"))
		all, err := s.List(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{all[0].ID, all[1].ID}).To(Equal([]string{"a", "b"}))
	})
	It("returns connections.ErrNotFound", func() {
		_, err := s.Get(ctx, "x")
		Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
		_, err = s.Update(ctx, "x", func(*connections.Record) error { return nil })
		Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
		Expect(errors.Is(s.Delete(ctx, "x"), connections.ErrNotFound)).To(BeTrue())
	})
	It("refuses a name that is taken, ignoring case", func() {
		Expect(s.Create(ctx, rec("a", "Home"))).To(Succeed())
		Expect(errors.Is(s.Create(ctx, rec("b", "home")), connections.ErrConflict)).To(BeTrue())
		Expect(errors.Is(s.Create(ctx, rec("a", "other")), connections.ErrConflict)).To(BeTrue(), "same id")
	})
	It("keeps the sealed key apart from the document and returns it", func() {
		r := rec("a", "home")
		r.Sealed = []byte{1, 2, 3}
		r.Owned = []connections.OwnedEntry{{Name: "m", UpstreamID: "u", Hash: "h"}}
		Expect(s.Create(ctx, r)).To(Succeed())
		got, err := s.Get(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Sealed).To(Equal([]byte{1, 2, 3}))
		Expect(got.Owned).To(Equal(r.Owned))
	})
	It("updates through fn and writes nothing when fn fails", func() {
		Expect(s.Create(ctx, rec("a", "home"))).To(Succeed())
		_, err := s.Update(ctx, "a", func(r *connections.Record) error { r.Name = "changed"; return errors.New("no") })
		Expect(err).To(HaveOccurred())
		got, _ := s.Get(ctx, "a")
		Expect(got.Name).To(Equal("home"))
		_, err = s.Update(ctx, "a", func(r *connections.Record) error { r.Name = "changed"; r.Status = connections.StatusOK; return nil })
		Expect(err).NotTo(HaveOccurred())
		got, _ = s.Get(ctx, "a")
		Expect(got.Name).To(Equal("changed"))
		Expect(got.Status).To(Equal(connections.StatusOK))
	})
	It("refuses a rename to a name that is taken", func() {
		Expect(s.Create(ctx, rec("a", "one"))).To(Succeed())
		Expect(s.Create(ctx, rec("b", "two"))).To(Succeed())
		_, err := s.Update(ctx, "b", func(r *connections.Record) error { r.Name = "ONE"; return nil })
		Expect(errors.Is(err, connections.ErrConflict)).To(BeTrue())
		got, _ := s.Get(ctx, "b")
		Expect(got.Name).To(Equal("two"))
	})
	It("does not lose changes of concurrent updates", func() {
		Expect(s.Create(ctx, rec("a", "home"))).To(Succeed())
		var wg sync.WaitGroup
		const n = 8
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, err := s.Update(ctx, "a", func(r *connections.Record) error {
					r.Owned = append(r.Owned, connections.OwnedEntry{Name: string(rune('a' + i))})
					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			}()
		}
		wg.Wait()
		got, _ := s.Get(ctx, "a")
		Expect(got.Owned).To(HaveLen(n))
	})
	It("deletes", func() {
		Expect(s.Create(ctx, rec("a", "home"))).To(Succeed())
		Expect(s.Delete(ctx, "a")).To(Succeed())
		_, err := s.Get(ctx, "a")
		Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
	})
	It("returns the same sealing key on every call", func() {
		calls := 0
		gen := func() ([]byte, error) { calls++; return connections.NewSealKey() }
		k1, err := s.EnsureKey(ctx, gen)
		Expect(err).NotTo(HaveOccurred())
		k2, err := s.EnsureKey(ctx, gen)
		Expect(err).NotTo(HaveOccurred())
		Expect(k2).To(Equal(k1))
		Expect(k1).To(HaveLen(connections.SealKeySize))
		Expect(calls).To(Equal(1))
	})
}

func openSQLite() *gorm.DB {
	path := filepath.Join(GinkgoT().TempDir(), "c.db")
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

var _ = Describe("stores", func() {
	Describe("JSON store in memory", func() {
		storeContract(func() connections.Store {
			s, err := connections.NewJSONStore("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("JSON store on disk", func() {
		storeContract(func() connections.Store {
			s, err := connections.NewJSONStore(filepath.Join(GinkgoT().TempDir(), "connections"))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("GORM store on SQLite", func() {
		storeContract(func() connections.Store {
			s, err := connections.NewGORMStore(openSQLite())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("GORM store on PostgreSQL", func() {
		// A container needs Docker, so it is opt in. A database URL that
		// tests may wipe works too.
		url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
		container := os.Getenv("LOCALAI_TEST_POSTGRES_CONTAINER") != ""
		if url == "" && !container {
			It("runs the store contract", func() {
				Skip("set LOCALAI_TEST_POSTGRES_URL or LOCALAI_TEST_POSTGRES_CONTAINER to run against PostgreSQL")
			})
			return
		}
		storeContract(func() connections.Store {
			var db *gorm.DB
			if url != "" {
				var err error
				db, err = gorm.Open(postgres.Open(url), &gorm.Config{})
				Expect(err).NotTo(HaveOccurred())
				s, err := connections.NewGORMStore(db)
				Expect(err).NotTo(HaveOccurred())
				Expect(db.Exec("DELETE FROM remote_connections").Error).To(Succeed())
				Expect(db.Exec("DELETE FROM remote_connection_keys").Error).To(Succeed())
				return s
			}
			db = testutil.SetupTestDB()
			s, err := connections.NewGORMStore(db)
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("JSON files", func() {
		var dir string
		BeforeEach(func() { dir = filepath.Join(GinkgoT().TempDir(), "connections") })

		It("writes the store and key files with mode 0600 and no leftover temp file", func() {
			s, err := connections.NewJSONStore(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Create(context.Background(), rec("a", "home"))).To(Succeed())
			_, err = s.EnsureKey(context.Background(), connections.NewSealKey)
			Expect(err).NotTo(HaveOccurred())
			for _, name := range []string{connections.JSONFileName, connections.JSONKeyName} {
				st, err := os.Stat(filepath.Join(dir, name))
				Expect(err).NotTo(HaveOccurred())
				Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)), name)
			}
			entries, _ := os.ReadDir(dir)
			Expect(entries).To(HaveLen(2))
		})
		It("survives a restart", func() {
			s, _ := connections.NewJSONStore(dir)
			Expect(s.Create(context.Background(), rec("a", "home"))).To(Succeed())
			again, _ := connections.NewJSONStore(dir)
			got, err := again.Get(context.Background(), "a")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("home"))
		})
		It("moves a damaged file aside and starts empty", func() {
			s, _ := connections.NewJSONStore(dir)
			Expect(os.WriteFile(filepath.Join(dir, connections.JSONFileName), []byte("{not json"), 0o600)).To(Succeed())
			all, err := s.List(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(BeEmpty())
			matches, _ := filepath.Glob(filepath.Join(dir, connections.JSONFileName+".corrupt-*"))
			Expect(matches).To(HaveLen(1))
			raw, _ := os.ReadFile(matches[0])
			Expect(string(raw)).To(Equal("{not json"))
			Expect(s.Create(context.Background(), rec("a", "home"))).To(Succeed())
		})
		It("refuses a key file that others can read", func() {
			s, _ := connections.NewJSONStore(dir)
			_, err := s.EnsureKey(context.Background(), connections.NewSealKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Chmod(filepath.Join(dir, connections.JSONKeyName), 0o644)).To(Succeed())
			_, err = s.EnsureKey(context.Background(), connections.NewSealKey)
			Expect(err).To(MatchError(ContainSubstring("chmod 600")))
		})
		It("refuses a key file of the wrong size", func() {
			s, _ := connections.NewJSONStore(dir)
			Expect(os.WriteFile(filepath.Join(dir, connections.JSONKeyName), []byte("short"), 0o600)).To(Succeed())
			_, err := s.EnsureKey(context.Background(), connections.NewSealKey)
			Expect(err).To(HaveOccurred())
		})
	})
})
