package audit_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
)

func TestAudit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "egress audit suite")
}

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func contract(open func() audit.Store) {
	ctx := context.Background()
	It("adds, filters, counts and prunes", func() {
		s := open()
		Expect(s.Add(ctx,
			audit.Event{User: "alice", Chat: "c1", Host: "pypi.org", Port: 443, Kind: "connect", Verdict: "allow", Reason: "preset", At: t0.Add(-48 * time.Hour)},
			audit.Event{User: "alice", Chat: "c1", Host: "evil.example", Port: 443, Kind: "connect", Verdict: "block", Reason: "off", At: t0},
			audit.Event{User: "bob", Chat: "c2", Host: "pypi.org", Port: 443, Kind: "connect", Verdict: "allow", Reason: "preset", BytesDown: 10, At: t0.Add(time.Minute)},
		)).To(Succeed())
		all, err := s.Events(ctx, audit.Query{})
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(3))
		Expect(all[0].User).To(Equal("bob"), "newest first")
		got, _ := s.Events(ctx, audit.Query{User: "alice"})
		Expect(got).To(HaveLen(2))
		got, _ = s.Events(ctx, audit.Query{Verdict: "block"})
		Expect(got).To(HaveLen(1))
		Expect(got[0].Host).To(Equal("evil.example"))
		got, _ = s.Events(ctx, audit.Query{Host: "pypi.org", Chat: "c2", Limit: 1})
		Expect(got).To(HaveLen(1))
		Expect(got[0].BytesDown).To(Equal(int64(10)))
		c, err := s.Count(ctx, t0.Add(-24*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(c).To(Equal(audit.Counts{Allowed: 1, Blocked: 1}))
		n, err := s.Prune(ctx, t0.Add(-24*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(1)))
		all, _ = s.Events(ctx, audit.Query{})
		Expect(all).To(HaveLen(2))
	})
}

var _ = Describe("the JSON audit", func() {
	contract(func() audit.Store {
		s, err := audit.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		return s
	})
	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		s, _ := audit.NewJSON(dir)
		Expect(s.Add(context.Background(), audit.Event{User: "a", Host: "h", Verdict: "allow", At: t0})).To(Succeed())
		s2, err := audit.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		got, _ := s2.Events(context.Background(), audit.Query{})
		Expect(got).To(HaveLen(1))
	})
})

var _ = Describe("the GORM audit on SQLite", func() {
	contract(func() audit.Store {
		db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "a.db"))
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		s, err := audit.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		return s
	})
})
