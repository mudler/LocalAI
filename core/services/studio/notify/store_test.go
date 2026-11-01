package notify_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

var _ = Describe("the JSON notification store", func() {
	Describe("in memory", func() {
		notifytest.Run(func() notify.Store {
			s, err := notify.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		notifytest.Run(func() notify.Store {
			s, err := notify.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
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
