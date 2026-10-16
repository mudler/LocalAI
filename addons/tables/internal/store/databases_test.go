// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	idA = "r0123456789abcdef0123456789abcde"
	idB = "r1123456789abcdef0123456789abcde"
)

func open(dir string, o store.Options) *store.Store {
	GinkgoHelper()
	s, e := store.Open(dir, o)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = s.Close() })
	return s
}

func files(dir string) []string {
	var out []string
	_ = filepath.Walk(dir, func(p string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() {
			r, _ := filepath.Rel(dir, p)
			out = append(out, r)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func ledgerExec(dir, query string, args ...any) {
	GinkgoHelper()
	db, e := sql.Open("sqlite3", filepath.Join(dir, "ledger.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	defer func() { _ = db.Close() }()
	_, e = db.Exec(query, args...)
	Expect(e).NotTo(HaveOccurred())
}

var _ = Describe("Tables databases", func() {
	var dir string
	var s *store.Store
	ctx := context.Background()
	BeforeEach(func() {
		dir = filepath.Join(GinkgoT().TempDir(), "tables")
		s = open(dir, store.Options{})
	})

	It("creates one private WAL database file named by the host resource ID", func() {
		info, e := s.Create(ctx, idA, "Sales", "op-create-a")
		Expect(e).NotTo(HaveOccurred())
		Expect(info.ResourceID).To(Equal(idA))
		Expect(info.Name).To(Equal("Sales"))
		Expect(info.Revision.Schema).To(BeNumerically(">=", 1))
		path := filepath.Join(dir, "databases", idA+".db")
		fi, e := os.Stat(path)
		Expect(e).NotTo(HaveOccurred())
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		di, e := os.Stat(filepath.Join(dir, "databases"))
		Expect(e).NotTo(HaveOccurred())
		Expect(di.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		root, _ := os.Stat(dir)
		Expect(root.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		db, e := sql.Open("sqlite3", "file:"+path+"?mode=ro")
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = db.Close() }()
		var mode string
		Expect(db.QueryRow("PRAGMA journal_mode").Scan(&mode)).To(Succeed())
		Expect(strings.ToLower(mode)).To(Equal("wal"))
		var name, rid string
		Expect(db.QueryRow("SELECT value FROM tables_meta WHERE key='name'").Scan(&name)).To(Succeed())
		Expect(db.QueryRow("SELECT value FROM tables_meta WHERE key='resource_id'").Scan(&rid)).To(Succeed())
		Expect(name).To(Equal("Sales"))
		Expect(rid).To(Equal(idA))
		got, e := s.Inspect(ctx, idA)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Sales"))
		Expect(got.ResourceID).To(Equal(idA))
		Expect(got.Tables).To(BeEmpty())
		ok, e := s.Exists(ctx, idA)
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	It("never lets an opaque ID or a name reach outside the storage directory", func() {
		parent := filepath.Dir(dir)
		for _, id := range []string{"", "../escape", "a/b", `a\b`, "..", ".", "R0", "r0.db", strings.Repeat("a", 65), "a\x00b", "-x", "ab cd"} {
			_, e := s.Create(ctx, id, "Name", "op-bad-id")
			Expect(e).To(MatchError(store.ErrInvalidID), "id %q", id)
			_, e = s.Inspect(ctx, id)
			Expect(e).To(MatchError(store.ErrInvalidID), "id %q", id)
			_, e = s.Rename(ctx, id, "Name", "op-bad-id")
			Expect(e).To(MatchError(store.ErrInvalidID), "id %q", id)
		}
		for _, name := range []string{"", "   ", strings.Repeat("x", 81), "tab\tname", "nul\x00"} {
			_, e := s.Create(ctx, idA, name, "op-bad-name")
			Expect(e).To(MatchError(store.ErrInvalidName), "name %q", name)
		}
		_, e := s.Create(ctx, idA, "Fine", "Bad Op")
		Expect(e).To(MatchError(store.ErrInvalidOperation))
		Expect(files(parent)).NotTo(ContainElement(ContainSubstring("escape")))
		Expect(files(filepath.Join(dir, "databases"))).To(BeEmpty())
	})

	It("refuses a symbolic link as storage directory, database file or ledger", func() {
		base := GinkgoT().TempDir()
		target := filepath.Join(base, "elsewhere")
		Expect(os.Mkdir(target, 0o700)).To(Succeed())
		link := filepath.Join(base, "linked")
		Expect(os.Symlink(target, link)).To(Succeed())
		_, e := store.Open(link, store.Options{})
		Expect(e).To(MatchError(store.ErrUnsafePath))

		_, e = s.Create(ctx, idA, "Real", "op-real")
		Expect(e).NotTo(HaveOccurred())
		outside := filepath.Join(base, "outside.db")
		Expect(os.WriteFile(outside, []byte("x"), 0o600)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(dir, "databases", idB+".db"))).To(Succeed())
		_, e = s.Inspect(ctx, idB)
		Expect(e).To(HaveOccurred())
		_, e = s.Create(ctx, idB, "Other", "op-other")
		Expect(e).To(HaveOccurred())
		_, e = s.Rename(ctx, idB, "Changed", "op-ren-link")
		Expect(e).To(HaveOccurred())
		b, _ := os.ReadFile(outside)
		Expect(string(b)).To(Equal("x"))
	})

	It("is idempotent by operation ID and never creates a second database", func() {
		a, e := s.Create(ctx, idA, "Sales", "op-1")
		Expect(e).NotTo(HaveOccurred())
		again, e := s.Create(ctx, idA, "Sales", "op-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(a))
		Expect(files(filepath.Join(dir, "databases"))).To(Equal([]string{idA + ".db"}))

		// The same operation ID cannot name another resource or another name.
		_, e = s.Create(ctx, idB, "Sales", "op-1")
		Expect(e).To(MatchError(store.ErrConflict))
		_, e = s.Create(ctx, idA, "Other", "op-1")
		Expect(e).To(MatchError(store.ErrConflict))
		// A second operation cannot take over an existing resource ID.
		_, e = s.Create(ctx, idA, "Hijack", "op-2")
		Expect(e).To(MatchError(store.ErrExists))
		got, _ := s.Inspect(ctx, idA)
		Expect(got.Name).To(Equal("Sales"))
		Expect(files(filepath.Join(dir, "databases"))).To(HaveLen(1))
	})

	It("serializes concurrent creation of one operation into one database", func() {
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = s.Create(ctx, idA, "Sales", "op-race")
			}()
		}
		wg.Wait()
		for _, e := range errs {
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(files(filepath.Join(dir, "databases"))).To(Equal([]string{idA + ".db"}))
	})

	It("recovers creation after a crash on either side of the file, and hides half-built files", func() {
		// Crash after the reservation, before any file: the retry creates it.
		ledgerExec(dir, "INSERT INTO operations(operation_id,kind,resource_id,name,state,schema_revision,data_revision) VALUES('op-pre','create',?,'Early','pending',0,0)", idA)
		ok, e := s.Exists(ctx, idA)
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		_, e = s.Receipt(ctx, "op-pre")
		Expect(e).NotTo(HaveOccurred())
		r, _ := s.Receipt(ctx, "op-pre")
		Expect(r.State).NotTo(Equal(contract.ReceiptCommitted))
		info, e := s.Create(ctx, idA, "Early", "op-pre")
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Name).To(Equal("Early"))

		// Crash after the file, before the ledger recorded the result.
		_, e = s.Create(ctx, idB, "Late", "op-post")
		Expect(e).NotTo(HaveOccurred())
		ledgerExec(dir, "UPDATE operations SET state='pending' WHERE operation_id='op-post'")
		s2 := open(dir, store.Options{})
		r, e = s2.Receipt(ctx, "op-post")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(contract.ReceiptPending))
		info, e = s2.Create(ctx, idB, "Late", "op-post")
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Name).To(Equal("Late"))
		r, _ = s2.Receipt(ctx, "op-post")
		Expect(r.State).To(Equal(contract.ReceiptCommitted))
		Expect(files(filepath.Join(dir, "databases"))).To(Equal([]string{idA + ".db", idB + ".db"}))

		// A half-built file is never a database and is removed on open.
		tmp := filepath.Join(dir, "databases", ".creating-r2")
		Expect(os.WriteFile(tmp, []byte("partial"), 0o600)).To(Succeed())
		s3 := open(dir, store.Options{})
		ok, _ = s3.Exists(ctx, "r2")
		Expect(ok).To(BeFalse())
		Expect(tmp).NotTo(BeAnExistingFile())
	})

	It("renames only the named database and replays a renamed operation without effect", func() {
		_, e := s.Create(ctx, idA, "Sales", "op-a")
		Expect(e).NotTo(HaveOccurred())
		_, e = s.Create(ctx, idB, "Costs", "op-b")
		Expect(e).NotTo(HaveOccurred())
		other, _ := os.ReadFile(filepath.Join(dir, "databases", idB+".db"))

		r, e := s.Rename(ctx, idA, "Revenue", "op-ren-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(contract.ReceiptCommitted))
		Expect(r.OperationID).To(Equal("op-ren-1"))
		Expect(r.Revision.Schema).To(BeNumerically(">=", 1))
		a, _ := s.Inspect(ctx, idA)
		b, _ := s.Inspect(ctx, idB)
		Expect(a.Name).To(Equal("Revenue"))
		Expect(b.Name).To(Equal("Costs"))
		after, _ := os.ReadFile(filepath.Join(dir, "databases", idB+".db"))
		Expect(after).To(Equal(other))

		_, e = s.Rename(ctx, idA, "Final", "op-ren-2")
		Expect(e).NotTo(HaveOccurred())
		replay, e := s.Rename(ctx, idA, "Revenue", "op-ren-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(r))
		a, _ = s.Inspect(ctx, idA)
		Expect(a.Name).To(Equal("Final"))
		// An operation ID is not portable to another database or name.
		_, e = s.Rename(ctx, idB, "Stolen", "op-ren-1")
		Expect(e).To(MatchError(store.ErrConflict))
		_, e = s.Rename(ctx, idA, "Different", "op-ren-1")
		Expect(e).To(MatchError(store.ErrConflict))
		b, _ = s.Inspect(ctx, idB)
		Expect(b.Name).To(Equal("Costs"))
	})

	It("reports an unknown database and creates nothing for it", func() {
		_, e := s.Inspect(ctx, idA)
		Expect(e).To(MatchError(store.ErrNotFound))
		_, e = s.Rename(ctx, idA, "X", "op-ghost")
		Expect(e).To(MatchError(store.ErrNotFound))
		ok, e := s.Exists(ctx, idA)
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(files(filepath.Join(dir, "databases"))).To(BeEmpty())
		r, e := s.Receipt(ctx, "op-ghost")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(contract.ReceiptUnknown))
	})

	It("honours the busy timeout and the per-operation deadline, then succeeds once the lock is gone", func() {
		_, e := s.Create(ctx, idA, "Sales", "op-a")
		Expect(e).NotTo(HaveOccurred())
		short := open(dir, store.Options{BusyTimeout: 100 * time.Millisecond, OpTimeout: 400 * time.Millisecond})
		lock, e := sql.Open("sqlite3", "file:"+filepath.Join(dir, "databases", idA+".db")+"?_txlock=exclusive")
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = lock.Close() }()
		tx, e := lock.Begin()
		Expect(e).NotTo(HaveOccurred())
		start := time.Now()
		_, e = short.Rename(ctx, idA, "Locked", "op-ren-lock")
		Expect(e).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
		r, _ := short.Receipt(ctx, "op-ren-lock")
		Expect(r.State).NotTo(Equal(contract.ReceiptCommitted))
		Expect(tx.Rollback()).To(Succeed())
		r, e = short.Rename(ctx, idA, "Locked", "op-ren-lock")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(contract.ReceiptCommitted))
	})

	It("encodes its receipts so the host can read them", func() {
		_, e := s.Create(ctx, idA, "Sales", "op-a")
		Expect(e).NotTo(HaveOccurred())
		r, e := s.Receipt(ctx, "op-a")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(contract.ReceiptCommitted))
		Expect(r.Validate(contract.DefaultLimits())).To(Succeed())
		var p struct {
			ResourceID string `json:"resourceId"`
			Name       string `json:"name"`
		}
		Expect(json.Unmarshal(r.Result.Payload, &p)).To(Succeed())
		Expect(p.ResourceID).To(Equal(idA))
		Expect(p.Name).To(Equal("Sales"))
	})
})
