// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("File store", func() {
	Context("contract with a file", func() {
		registerStoreContract(func() Store {
			s, err := NewFileStore(filepath.Join(GinkgoT().TempDir(), "pins.json"))
			Expect(err).ToNot(HaveOccurred())
			return s
		})
	})

	Context("contract in memory only", func() {
		registerStoreContract(func() Store {
			s, err := NewFileStore("")
			Expect(err).ToNot(HaveOccurred())
			return s
		})
	})

	It("keeps pins across a reopen", func() {
		ctx := context.Background()
		path := filepath.Join(GinkgoT().TempDir(), "pins.json")
		a, _ := NewFileStore(path)
		ran, _ := a.SeedExisting(ctx, []string{"local-owner"}, SeedIDs)
		Expect(ran).To(BeTrue(), "first seed must run")
		_ = a.Replace(ctx, "local-owner", []string{"talk"})
		b, err := NewFileStore(path)
		Expect(err).ToNot(HaveOccurred())
		got, _ := b.List(ctx, "local-owner")
		Expect(idsOf(got)).To(Equal([]string{"talk"}), "pins lost across a restart")
		again, _ := b.SeedExisting(ctx, []string{"local-owner"}, SeedIDs)
		Expect(again).To(BeFalse(), "the seed marker must survive a restart")
	})

	// The retention window of a removed add-on must not restart at each reopen.
	It("keeps the missing mark and the owner across a reopen", func() {
		ctx := context.Background()
		path := filepath.Join(GinkgoT().TempDir(), "pins.json")
		a, _ := NewFileStore(path)
		_ = a.Replace(ctx, "local-owner", []string{"notes", "studio"})
		mark := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
		Expect(a.MarkMissing(ctx, []string{"notes"}, mark)).To(Succeed())
		b, err := NewFileStore(path)
		Expect(err).ToNot(HaveOccurred())
		got, _ := b.List(ctx, "local-owner")
		Expect(got).To(HaveLen(2), "the owner of a pin was lost: %+v", got)
		Expect(got[0].UserID).To(Equal("local-owner"), "the owner of a pin was lost: %+v", got)
		Expect(got[0].MissingSince).ToNot(BeNil(), "the start of the retention window was lost across a restart")
		Expect(got[0].MissingSince.Equal(mark)).To(BeTrue(), "the start of the retention window was lost across a restart: %v", got[0].MissingSince)
	})
})
