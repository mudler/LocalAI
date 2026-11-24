package hostexec_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var _ = Describe("the retention of the audit", func() {
	stores := map[string]func() hostexec.AuditStore{
		"gorm": func() hostexec.AuditStore {
			db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "audit.db")+"?_busy_timeout=5000", &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if raw, err := db.DB(); err == nil {
					_ = raw.Close()
				}
			})
			st, err := hostexec.NewGORMAudit(db)
			Expect(err).NotTo(HaveOccurred())
			return st
		},
		"file": func() hostexec.AuditStore {
			st, err := hostexec.NewJSONAudit(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return st
		},
	}
	for name, open := range stores {
		It("prunes the lines older than the cut-off in the "+name+" store", func() {
			ctx := context.Background()
			st := open()
			now := time.Now().UTC().Truncate(time.Second)
			Expect(st.Add(ctx, hostexec.Event{User: "a", Command: "old", At: now.Add(-40 * 24 * time.Hour)})).To(Succeed())
			Expect(st.Add(ctx, hostexec.Event{User: "a", Command: "new", At: now})).To(Succeed())
			n, err := st.Prune(ctx, now.Add(-30*24*time.Hour))
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(1)))
			evs, err := st.Events(ctx, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(evs).To(HaveLen(1))
			Expect(evs[0].Command).To(Equal("new"))
			n, err = st.Prune(ctx, now.Add(-30*24*time.Hour))
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(BeZero(), "a second prune changes nothing")
		})
	}
})
