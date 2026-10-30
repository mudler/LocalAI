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

var _ = Describe("Sweep of the seeded pins", func() {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	It("keeps a seeded pin while its add-on was never installed, then applies the rule", func() {
		s, _ := NewFileStore("")
		_, err := s.SeedExisting(ctx, []string{"u1"}, SeedIDs)
		Expect(err).ToNot(HaveOccurred())
		// Ninety days with Studio and Talk not installed: nothing expires.
		for d := 0; d <= 90; d += 10 {
			n, err := Sweep(ctx, s, []string{"notes"}, t0.Add(time.Duration(d)*day))
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeZero(), "day %d", d)
		}
		got, _ := s.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"studio", "talk"}))
		for _, p := range got {
			Expect(p.MissingSince).To(BeNil(), p.AddonID)
		}

		// Studio is installed, then removed: from then on its pin expires
		// after Retention. Talk, never installed, stays.
		_, _ = Sweep(ctx, s, []string{"studio"}, t0.Add(100*day))
		Expect(s.Installed(ctx)).To(Equal([]string{"studio"}))
		_, _ = Sweep(ctx, s, []string{"notes"}, t0.Add(101*day))
		n, _ := Sweep(ctx, s, []string{"notes"}, t0.Add(101*day).Add(Retention).Add(time.Hour))
		Expect(n).To(BeEquivalentTo(1))
		got, _ = s.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"talk"}))
	})

	It("clears the mark an older sweep set on a seeded pin that was never installed", func() {
		s, _ := NewFileStore("")
		_ = s.Replace(ctx, "u1", []string{"talk"})
		Expect(s.MarkMissing(ctx, []string{"talk"}, t0)).To(Succeed())
		_, err := Sweep(ctx, s, []string{"notes"}, t0.Add(day))
		Expect(err).ToNot(HaveOccurred())
		got, _ := s.List(ctx, "u1")
		Expect(got).To(HaveLen(1))
		Expect(got[0].MissingSince).To(BeNil())
	})
})
