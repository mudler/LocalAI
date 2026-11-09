package notify_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

var _ = Describe("the JSON notification store", func() {
	Describe("in memory", func() {
		mem := func() notify.Store {
			s, err := notify.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		}
		notifytest.Run(mem)
		notifytest.RunKeyed(mem)
	})

	Describe("on disk", func() {
		disk := func() notify.Store {
			s, err := notify.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		}
		notifytest.Run(disk)
		notifytest.RunKeyed(disk)
	})

	It("keeps a hidden keyed row across a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := notify.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		got, _, err := first.Upsert(context.Background(), notify.Notification{Principal: "u1", Key: "k", Title: "t", Ids: []string{"a"}, Created: time.Now()}, notify.ModeSet)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Delete(context.Background(), "u1", []int64{got.ID})).To(Succeed())
		second, err := notify.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		_, res, _ := second.Upsert(context.Background(), notify.Notification{Principal: "u1", Key: "k", Title: "t", Ids: []string{"a"}, Created: time.Now()}, notify.ModeSet)
		Expect(res).To(Equal(notify.ResultUnchanged))
	})

	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := notify.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		notifytest.Seed(first)
		second, err := notify.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		notifytest.CheckSeed(second)
	})
})
