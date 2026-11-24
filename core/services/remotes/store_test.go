package remotes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/remotes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/fsperm"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func rec(id, name string) remotes.Record {
	return remotes.Record{ID: id, Name: name, Kind: remotes.KindLocalAI, URL: "https://remote.example.com", Status: remotes.StatusNever, Created: time.Now().UTC()}
}

// storeContract is what every remotes.Store must do.
func storeContract(newStore func() remotes.Store) {
	var s remotes.Store
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
	It("returns remotes.ErrNotFound", func() {
		_, err := s.Get(ctx, "x")
		Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
		_, err = s.Update(ctx, "x", func(*remotes.Record) error { return nil })
		Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
		Expect(errors.Is(s.Delete(ctx, "x", nil), remotes.ErrNotFound)).To(BeTrue())
	})
	It("refuses a name that is taken, ignoring case", func() {
		Expect(s.Create(ctx, rec("a", "Home"))).To(Succeed())
		Expect(errors.Is(s.Create(ctx, rec("b", "home")), remotes.ErrConflict)).To(BeTrue())
		Expect(errors.Is(s.Create(ctx, rec("a", "other")), remotes.ErrConflict)).To(BeTrue(), "same id")
	})
	It("keeps the sealed key apart from the document and returns it", func() {
		r := rec("a", "home")
		r.Sealed = []byte{1, 2, 3}
		r.Owned = []remotes.OwnedEntry{{Name: "m", UpstreamID: "u", Hash: "h"}}
		Expect(s.Create(ctx, r)).To(Succeed())
		got, err := s.Get(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Sealed).To(Equal([]byte{1, 2, 3}))
		Expect(got.Owned).To(Equal(r.Owned))
	})
	It("updates through fn and writes nothing when fn fails", func() {
		Expect(s.Create(ctx, rec("a", "home"))).To(Succeed())
		_, err := s.Update(ctx, "a", func(r *remotes.Record) error { r.Name = "changed"; return errors.New("no") })
		Expect(err).To(HaveOccurred())
		got, _ := s.Get(ctx, "a")
		Expect(got.Name).To(Equal("home"))
		_, err = s.Update(ctx, "a", func(r *remotes.Record) error { r.Name = "changed"; r.Status = remotes.StatusOK; return nil })
		Expect(err).NotTo(HaveOccurred())
		got, _ = s.Get(ctx, "a")
		Expect(got.Name).To(Equal("changed"))
		Expect(got.Status).To(Equal(remotes.StatusOK))
	})
	It("refuses a rename to a name that is taken", func() {
		Expect(s.Create(ctx, rec("a", "one"))).To(Succeed())
		Expect(s.Create(ctx, rec("b", "two"))).To(Succeed())
		_, err := s.Update(ctx, "b", func(r *remotes.Record) error { r.Name = "ONE"; return nil })
		Expect(errors.Is(err, remotes.ErrConflict)).To(BeTrue())
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
				_, err := s.Update(ctx, "a", func(r *remotes.Record) error {
					r.Owned = append(r.Owned, remotes.OwnedEntry{Name: string(rune('a' + i))})
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
		Expect(s.Delete(ctx, "a", nil)).To(Succeed())
		_, err := s.Get(ctx, "a")
		Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
	})
	It("keeps the record when the delete check fails, in the same step", func() {
		r := rec("a", "home")
		r.Owned = []remotes.OwnedEntry{{Name: "m", UpstreamID: "u"}}
		Expect(s.Create(ctx, r)).To(Succeed())
		owned := errors.New("still owns entries")
		err := s.Delete(ctx, "a", func(r remotes.Record) error {
			if len(r.Owned) > 0 {
				return owned
			}
			return nil
		})
		Expect(err).To(MatchError(owned))
		_, err = s.Get(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Delete(ctx, "a", func(remotes.Record) error { return nil })).To(Succeed())
		Expect(errors.Is(s.Delete(ctx, "a", func(remotes.Record) error { return nil }), remotes.ErrNotFound)).To(BeTrue())
	})
	It("refuses to make a new sealing key while sealed records exist", func() {
		r := rec("a", "home")
		r.Sealed = []byte{1, 2, 3}
		Expect(s.Create(ctx, r)).To(Succeed())
		called := false
		_, err := s.EnsureKey(ctx, func() ([]byte, error) { called = true; return remotes.NewSealKey() })
		Expect(err).To(MatchError(ContainSubstring("sealing key is missing")))
		Expect(called).To(BeFalse())
	})
	It("returns the same sealing key on every call", func() {
		calls := 0
		gen := func() ([]byte, error) { calls++; return remotes.NewSealKey() }
		k1, err := s.EnsureKey(ctx, gen)
		Expect(err).NotTo(HaveOccurred())
		k2, err := s.EnsureKey(ctx, gen)
		Expect(err).NotTo(HaveOccurred())
		Expect(k2).To(Equal(k1))
		Expect(k1).To(HaveLen(remotes.SealKeySize))
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
		storeContract(func() remotes.Store {
			s, err := remotes.NewJSONStore("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("JSON store on disk", func() {
		storeContract(func() remotes.Store {
			s, err := remotes.NewJSONStore(filepath.Join(GinkgoT().TempDir(), "remotes"))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("GORM store on SQLite", func() {
		storeContract(func() remotes.Store {
			s, err := remotes.NewGORMStore(openSQLite())
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
		storeContract(func() remotes.Store {
			var db *gorm.DB
			if url != "" {
				var err error
				db, err = gorm.Open(postgres.Open(url), &gorm.Config{})
				Expect(err).NotTo(HaveOccurred())
				s, err := remotes.NewGORMStore(db)
				Expect(err).NotTo(HaveOccurred())
				Expect(db.Exec("DELETE FROM proxy_remotes").Error).To(Succeed())
				Expect(db.Exec("DELETE FROM proxy_remote_keys").Error).To(Succeed())
				return s
			}
			db = testutil.SetupTestDB()
			s, err := remotes.NewGORMStore(db)
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("JSON files", func() {
		var dir string
		BeforeEach(func() { dir = filepath.Join(GinkgoT().TempDir(), "remotes") })

		It("writes the store and key files with mode 0600 and no leftover temp file", func() {
			s, err := remotes.NewJSONStore(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Create(context.Background(), rec("a", "home"))).To(Succeed())
			_, err = s.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).NotTo(HaveOccurred())
			for _, name := range []string{remotes.JSONFileName, remotes.JSONKeyName} {
				st, err := os.Stat(filepath.Join(dir, name))
				Expect(err).NotTo(HaveOccurred())
				if runtime.GOOS != "windows" {
					Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)), name)
				}
				Expect(fsperm.Check(filepath.Join(dir, name))).To(Succeed(), name)
			}
			entries, _ := os.ReadDir(dir)
			Expect(entries).To(HaveLen(2))
		})
		It("survives a restart", func() {
			s, _ := remotes.NewJSONStore(dir)
			Expect(s.Create(context.Background(), rec("a", "home"))).To(Succeed())
			again, _ := remotes.NewJSONStore(dir)
			got, err := again.Get(context.Background(), "a")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("home"))
		})
		It("moves a damaged file aside and starts empty", func() {
			s, _ := remotes.NewJSONStore(dir)
			Expect(os.WriteFile(filepath.Join(dir, remotes.JSONFileName), []byte("{not json"), 0o600)).To(Succeed())
			all, err := s.List(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(BeEmpty())
			matches, _ := filepath.Glob(filepath.Join(dir, remotes.JSONFileName+".corrupt-*"))
			Expect(matches).To(HaveLen(1))
			raw, _ := os.ReadFile(matches[0])
			Expect(string(raw)).To(Equal("{not json"))
			Expect(s.Create(context.Background(), rec("a", "home"))).To(Succeed())
		})
		It("reuses its key file after a restart", func() {
			s, _ := remotes.NewJSONStore(dir)
			first, err := s.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).NotTo(HaveOccurred())
			again, err := remotes.NewJSONStore(dir)
			Expect(err).NotTo(HaveOccurred())
			second, err := again.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(second).To(Equal(first))
		})
		It("refuses a key file that others can read", func() {
			if runtime.GOOS == "windows" {
				Skip("Windows has no mode bits; pkg/fsperm tests the access control list")
			}
			s, _ := remotes.NewJSONStore(dir)
			_, err := s.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Chmod(filepath.Join(dir, remotes.JSONKeyName), 0o644)).To(Succeed())
			_, err = s.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).To(MatchError(ContainSubstring("chmod 600")))
		})
		It("refuses to replace a lost key file while remotes.json holds sealed keys", func() {
			s, _ := remotes.NewJSONStore(dir)
			_, err := s.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).NotTo(HaveOccurred())
			r := rec("a", "home")
			r.Sealed = []byte{1, 2, 3}
			Expect(s.Create(context.Background(), r)).To(Succeed())
			Expect(os.Remove(filepath.Join(dir, remotes.JSONKeyName))).To(Succeed())
			again, _ := remotes.NewJSONStore(dir)
			_, err = again.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).To(MatchError(ContainSubstring("sealing key is missing")))
			Expect(filepath.Join(dir, remotes.JSONKeyName)).NotTo(BeAnExistingFile())
		})
		It("closes a directory and a store file that others can read", func() {
			if runtime.GOOS == "windows" {
				Skip("Windows has no mode bits; pkg/fsperm tests the access control list")
			}
			Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
			Expect(os.Chmod(dir, 0o755)).To(Succeed())
			s, err := remotes.NewJSONStore(dir)
			Expect(err).NotTo(HaveOccurred())
			st, _ := os.Stat(dir)
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o700)))
			Expect(s.Create(context.Background(), rec("a", "home"))).To(Succeed())
			file := filepath.Join(dir, remotes.JSONFileName)
			Expect(os.Chmod(file, 0o644)).To(Succeed())
			_, err = s.List(context.Background())
			Expect(err).NotTo(HaveOccurred())
			st, _ = os.Stat(file)
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})
		It("refuses a key file of the wrong size", func() {
			s, _ := remotes.NewJSONStore(dir)
			Expect(os.WriteFile(filepath.Join(dir, remotes.JSONKeyName), []byte("short"), 0o600)).To(Succeed())
			_, err := s.EnsureKey(context.Background(), remotes.NewSealKey)
			Expect(err).To(HaveOccurred())
		})
	})
})
