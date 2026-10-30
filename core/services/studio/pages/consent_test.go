package pages_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var _ = Describe("consent stores", func() {
	ctx := context.Background()
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

	stores := map[string]func() pages.ConsentStore{
		"gorm": func() pages.ConsentStore {
			db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "c.db")+"?_busy_timeout=5000", &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if raw, err := db.DB(); err == nil {
					_ = raw.Close()
				}
			})
			s, err := pages.NewGORMConsents(db)
			Expect(err).NotTo(HaveOccurred())
			return s
		},
		"file": func() pages.ConsentStore {
			s, err := pages.NewJSONConsents(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		},
	}
	for name, open := range stores {
		It(name+": keeps one lasting consent per viewer and page, visits by token, and the audit", func() {
			s := open()
			c := pages.Consent{Principal: "marta", PageID: 7, Folder: "/shared/team", Name: "standup", Hash: "h1", Kind: pages.ConsentAlways, Size: 10, At: at}
			Expect(s.Put(ctx, c, pages.ConsentEvent{Principal: "marta", PageID: 7, Action: "allow", Hash: "h1", At: at})).To(Succeed())
			c.Hash = "h2"
			Expect(s.Put(ctx, c, pages.ConsentEvent{Principal: "marta", PageID: 7, Action: "allow", Hash: "h2", At: at})).To(Succeed())
			got, ok, err := s.Always(ctx, "marta", 7)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(got.Hash).To(Equal("h2"), "a new consent replaces the old one")
			_, ok, _ = s.Always(ctx, "sara", 7)
			Expect(ok).To(BeFalse())
			_, ok, _ = s.Always(ctx, "marta", 8)
			Expect(ok).To(BeFalse())

			once := pages.Consent{Principal: "marta", PageID: 8, Hash: "h3", Kind: pages.ConsentOnce, Token: "tok", At: at, Expires: time.Now().Add(time.Hour)}
			Expect(s.Put(ctx, once, pages.ConsentEvent{Principal: "marta", PageID: 8, Action: "allow_once", At: at})).To(Succeed())
			v, ok, _ := s.Once(ctx, "marta", "tok")
			Expect(ok).To(BeTrue())
			Expect(v.PageID).To(Equal(int64(8)))
			_, ok, _ = s.Once(ctx, "sara", "tok")
			Expect(ok).To(BeFalse(), "another viewer's token")
			_, ok, _ = s.Once(ctx, "marta", "")
			Expect(ok).To(BeFalse())

			Expect(s.Revoke(ctx, "marta", 7, pages.ConsentEvent{Principal: "marta", PageID: 7, Action: "revoke", At: at})).To(Succeed())
			_, ok, _ = s.Always(ctx, "marta", 7)
			Expect(ok).To(BeFalse())
			evs, err := s.Events(ctx, "marta", 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(evs).To(HaveLen(4))
			Expect(evs[0].Action).To(Equal("revoke"))
			Expect(evs[3].Hash).To(Equal("h1"))
		})
	}

	It("file: the consents outlive the process", func() {
		dir := GinkgoT().TempDir()
		s, err := pages.NewJSONConsents(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Put(ctx, pages.Consent{Principal: "marta", PageID: 7, Hash: "h1", Kind: pages.ConsentAlways, At: at}, pages.ConsentEvent{Action: "allow"})).To(Succeed())
		again, err := pages.NewJSONConsents(dir)
		Expect(err).NotTo(HaveOccurred())
		_, ok, _ := again.Always(ctx, "marta", 7)
		Expect(ok).To(BeTrue())
	})
})
