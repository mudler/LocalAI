// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sweep", func() {
	var (
		ctx context.Context
		s   Store
	)

	BeforeEach(func() {
		ctx = context.Background()
		s, _ = NewFileStore("")
	})

	It("keeps present IDs regardless of enablement and treats nil as a complete no-op", func() {
		Expect(s.Replace(ctx, "u1", []string{"studio", "talk", "arbitrary"})).To(Succeed())
		now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		Expect(s.MarkMissing(ctx, []string{"studio", "talk", "arbitrary"}, now)).To(Succeed())
		Expect(Sweep(ctx, s, nil, now.Add(2*Retention))).To(BeZero())
		got, err := s.List(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3))
		for _, pin := range got {
			Expect(pin.MissingSince).To(Equal(&now))
		}
		// Presence includes installed but disabled add-ons; enablement is not an input.
		Expect(Sweep(ctx, s, []string{"studio", "talk", "arbitrary"}, now.Add(2*Retention))).To(BeZero())
		got, err = s.List(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3))
		for _, pin := range got {
			Expect(pin.MissingSince).To(BeNil())
		}
	})

	It("applies equal retention to never-installed Studio Talk and arbitrary IDs", func() {
		Expect(s.Replace(ctx, "u1", []string{"studio", "talk", "arbitrary"})).To(Succeed())
		now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		n, err := Sweep(ctx, s, []string{}, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero())
		got, err := s.List(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		for _, pin := range got {
			Expect(pin.MissingSince).To(Equal(&now), pin.AddonID)
		}
		n, err = Sweep(ctx, s, []string{}, now.Add(Retention+time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(3))
		Expect(s.List(ctx, "u1")).To(BeEmpty())
	})

	It("keeps a removed pin for thirty days and restores it on reinstall", func() {
		_ = s.Replace(ctx, "u1", []string{"studio", "notes"})
		t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

		// notes is uninstalled: the pin is marked, not deleted.
		n, err := Sweep(ctx, s, []string{"studio"}, t0)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeZero(), "first sweep")
		// Day 29: still kept.
		n, _ = Sweep(ctx, s, []string{"studio"}, t0.Add(29*24*time.Hour))
		Expect(n).To(BeZero(), "deleted inside the window")
		// The add-on is installed again on day 29: the mark is cleared.
		_, _ = Sweep(ctx, s, []string{"studio", "notes"}, t0.Add(29*24*time.Hour))
		n, _ = Sweep(ctx, s, []string{"studio"}, t0.Add(40*24*time.Hour))
		Expect(n).To(BeZero(), "the window must restart after a reinstall")
		// 31 days after the new start it expires.
		n, _ = Sweep(ctx, s, []string{"studio"}, t0.Add(40*24*time.Hour).Add(Retention).Add(time.Hour))
		Expect(n).To(BeEquivalentTo(1), "expected the expired pin to be deleted")
		got, _ := s.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"studio"}))
	})

	It("never deletes a pin when the present list is empty due to an outage", func() {
		_ = s.Replace(ctx, "u1", []string{"notes"})
		// A nil present list means the caller could not read the owner: do nothing.
		n, err := Sweep(ctx, s, nil, time.Now().Add(365*24*time.Hour))
		Expect(err).ToNot(HaveOccurred(), "a sweep without an answer must not delete")
		Expect(n).To(BeZero(), "a sweep without an answer must not delete")
		got, _ := s.List(ctx, "u1")
		Expect(got).To(HaveLen(1))
		Expect(got[0].MissingSince).To(BeNil(), "a sweep without an answer must not mark either")
	})
})

var _ = Describe("Sweep retention marks", func() {
	It("preserves a never-installed pin's existing missing mark", func() {
		ctx := context.Background()
		s, err := NewFileStore("")
		Expect(err).NotTo(HaveOccurred())
		now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		Expect(s.Replace(ctx, "u1", []string{"talk"})).To(Succeed())
		Expect(s.MarkMissing(ctx, []string{"talk"}, now)).To(Succeed())
		Expect(Sweep(ctx, s, []string{}, now.Add(time.Hour))).To(BeZero())
		got, err := s.List(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].MissingSince).To(Equal(&now))
	})
})
