// SPDX-License-Identifier: MIT
package addons

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("owner selection epochs", func() {
	It("tracks published lifecycle ABA but not reads or idempotent commands", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		before := m.SelectionEpoch()
		_, err := m.Submit(ctx, actor, command("epoch-install", "install"))
		Expect(err).NotTo(HaveOccurred())
		installed := m.SelectionEpoch()
		Expect(installed).To(BeNumerically(">", before))
		_, err = m.Status(ctx, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.SelectionEpoch()).To(Equal(installed))
		_, err = m.Submit(ctx, actor, command("epoch-install", "install"))
		Expect(err).NotTo(HaveOccurred())
		Expect(m.SelectionEpoch()).To(Equal(installed))
		_, err = m.Submit(ctx, actor, command("epoch-enable", "enable"))
		Expect(err).NotTo(HaveOccurred())
		enabled := m.SelectionEpoch()
		_, err = m.Submit(ctx, actor, command("epoch-disable", "disable"))
		Expect(err).NotTo(HaveOccurred())
		_, err = m.Submit(ctx, actor, command("epoch-reenable", "enable"))
		Expect(err).NotTo(HaveOccurred())
		Expect(m.SelectionEpoch()).To(BeNumerically(">", enabled))
		stable := m.SelectionEpoch()
		_, err = m.Submit(ctx, actor, command("epoch-still-enabled", "enable"))
		Expect(err).NotTo(HaveOccurred())
		Expect(m.SelectionEpoch()).To(Equal(stable))
	})
	It("tracks recovery quiescence ABA and ignores repeated release and reads", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		h := &managerHost{m: m}
		first := m.SelectionEpoch()
		q, err := h.Quiesce(ctx, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.SelectionEpoch()).To(Equal(first + 1))
		_, err = h.Info(ctx, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.SelectionEpoch()).To(Equal(first + 1))
		q.Release()
		Expect(m.SelectionEpoch()).To(Equal(first + 2))
		q.Release()
		Expect(m.SelectionEpoch()).To(Equal(first + 2))
		q, err = h.Quiesce(ctx, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(q.Resume(ctx)).To(Succeed())
		Expect(m.SelectionEpoch()).To(Equal(first + 4))
	})

})
