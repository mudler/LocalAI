//go:build auth

// SPDX-License-Identifier: MIT
package pins

import (
	"context"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Two frontends over one database", func() {
	// Two frontends of one deployment share the database, so a save through one is
	// read through the other. No process holds the only copy.
	It("agree on the saved pins", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		a, _ := NewGormStore(db)
		b, _ := NewGormStore(db)
		ctx := context.Background()
		sa, sb := &Service{Store: a}, &Service{Store: b}
		vis := map[string]bool{"studio": true, "talk": true, "x": true}
		_, err = sa.Put(ctx, "u1", []string{"x", "talk"}, vis)
		Expect(err).ToNot(HaveOccurred())
		got, _ := sb.List(ctx, "u1", vis)
		Expect(got).To(Equal([]string{"x", "talk"}), "the second frontend sees a different list")
	})

	// Two frontends that start together run the upgrade seed once between them.
	It("seed once when they start together", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		a, _ := NewGormStore(db)
		b, _ := NewGormStore(db)
		ctx := context.Background()
		ranA, errA := a.SeedExisting(ctx, []string{"u1"}, SeedIDs)
		ranB, errB := b.SeedExisting(ctx, []string{"u1"}, SeedIDs)
		Expect(errA).ToNot(HaveOccurred())
		Expect(errB).ToNot(HaveOccurred())
		Expect(ranA).ToNot(Equal(ranB), "exactly one frontend must seed")
		got, _ := a.List(ctx, "u1")
		Expect(idsOf(got)).To(Equal([]string{"studio", "talk"}), "seeded twice or not at all")
	})
})
