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
