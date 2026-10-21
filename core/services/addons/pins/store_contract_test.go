// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func idsOf(ps []Pin) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.AddonID)
	}
	return out
}

// registerStoreContract registers the shared store specs. Call it inside a
// Describe once per store implementation; open returns a fresh empty store.
func registerStoreContract(open func() Store) {
	var (
		ctx context.Context
		s   Store
	)

	BeforeEach(func() {
		ctx = context.Background()
		s = open()
	})

	It("replace and list round-trip in order", func() {
		Expect(s.Replace(ctx, "u1", []string{"b", "a", "c"})).To(Succeed())
		got, _ := s.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"b", "a", "c"}), "order lost")
		Expect(s.Replace(ctx, "u1", []string{"c", "b"})).To(Succeed())
		got, _ = s.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"c", "b"}), "replace must drop what is not listed")
	})

	It("a second user does not see the first user's pins", func() {
		_ = s.Replace(ctx, "u1", []string{"a"})
		got, _ := s.List(ctx, "u2")
		Expect(got).To(BeEmpty(), "leak: %v", idsOf(got))
	})

	It("the upgrade seed pins existing accounts once and skips later ones", func() {
		ran, err := s.SeedExisting(ctx, []string{"u1", "u2"}, SeedIDs)
		Expect(err).ToNot(HaveOccurred())
		Expect(ran).To(BeTrue())
		for _, u := range []string{"u1", "u2"} {
			got, _ := s.List(ctx, u)
			Expect(idsOf(got)).To(Equal([]string{"studio", "talk"}), "account %s that existed at the upgrade must have both pins", u)
		}
		// An account created after the upgrade has no pins.
		got, _ := s.List(ctx, "u3")
		Expect(got).To(BeEmpty(), "a later account must start empty")
		_ = s.Replace(ctx, "u1", []string{"talk"}) // u1 unpins studio
		again, err := s.SeedExisting(ctx, []string{"u1", "u2", "u3"}, SeedIDs)
		Expect(err).ToNot(HaveOccurred())
		Expect(again).To(BeFalse(), "the seed ran twice")
		got, _ = s.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"talk"}), "an unpin must stick")
		got, _ = s.List(ctx, "u3")
		Expect(got).To(BeEmpty(), "a second run must not seed a later account")
	})

	It("a deployment with no account still records the seed, so a later account starts empty", func() {
		ran, err := s.SeedExisting(ctx, nil, SeedIDs)
		Expect(err).ToNot(HaveOccurred())
		Expect(ran).To(BeTrue(), "an empty first run must count")
		again, _ := s.SeedExisting(ctx, []string{"late"}, SeedIDs)
		Expect(again).To(BeFalse(), "the seed must not run after the first call")
		got, _ := s.List(ctx, "late")
		Expect(got).To(BeEmpty(), "a new account must start with no pins")
	})

	It("missing pins are marked, cleared and deleted after the window", func() {
		_ = s.Replace(ctx, "u1", []string{"gone", "here"})
		now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		Expect(s.MarkMissing(ctx, []string{"gone"}, now)).To(Succeed())
		// A second mark must not move the start of the window.
		Expect(s.MarkMissing(ctx, []string{"gone"}, now.Add(24*time.Hour))).To(Succeed())
		got, _ := s.List(ctx, "u1")
		for _, p := range got {
			if p.AddonID == "gone" {
				Expect(p.MissingSince).ToNot(BeNil(), "the window start moved")
				Expect(p.MissingSince.Equal(now)).To(BeTrue(), "the window start moved: %v", p.MissingSince)
			}
		}
		n, _ := s.DeleteMissingBefore(ctx, now.Add(-time.Second))
		Expect(n).To(BeZero(), "deleted inside the window")
		Expect(s.ClearMissing(ctx, []string{"gone"})).To(Succeed())
		_ = s.MarkMissing(ctx, []string{"gone"}, now)
		n, _ = s.DeleteMissingBefore(ctx, now.Add(time.Second))
		Expect(n).To(BeEquivalentTo(1), "expected one deleted pin")
		got, _ = s.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"here"}))
	})

	It("replace keeps the missing mark of a pin that stays", func() {
		_ = s.Replace(ctx, "u1", []string{"gone"})
		now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		_ = s.MarkMissing(ctx, []string{"gone"}, now)
		_ = s.Replace(ctx, "u1", []string{"gone", "new"})
		got, _ := s.List(ctx, "u1")
		Expect(got).ToNot(BeEmpty())
		Expect(got[0].MissingSince).ToNot(BeNil(), "a save must not reset the retention window")
	})

	It("distinct lists every pinned id once", func() {
		_ = s.Replace(ctx, "u1", []string{"a", "b"})
		_ = s.Replace(ctx, "u2", []string{"b", "c"})
		d, _ := s.Distinct(ctx)
		slices.Sort(d)
		Expect(d).To(Equal([]string{"a", "b", "c"}))
	})
}
