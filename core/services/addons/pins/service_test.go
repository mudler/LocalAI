// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func vis(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func newService() *Service {
	s, err := NewFileStore("")
	Expect(err).ToNot(HaveOccurred())
	return &Service{Store: s}
}

var _ = Describe("Service", func() {
	var (
		ctx context.Context
		svc *Service
	)

	BeforeEach(func() {
		ctx = context.Background()
		svc = newService()
	})

	It("starts a new account with no pins", func() {
		// The upgrade ran earlier for the accounts that existed then.
		ran, err := svc.Store.SeedExisting(ctx, []string{"old"}, SeedIDs)
		Expect(err).ToNot(HaveOccurred())
		Expect(ran).To(BeTrue())
		got, err := svc.List(ctx, "created-after-the-upgrade", vis("studio", "talk"))
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty(), "a new account must start with no pins")
		_, err = svc.Put(ctx, "created-after-the-upgrade", []string{"talk"}, vis("studio", "talk"))
		Expect(err).ToNot(HaveOccurred())
		got, _ = svc.List(ctx, "created-after-the-upgrade", vis("studio", "talk"))
		Expect(slices.Equal(got, []string{"talk"})).To(BeTrue(), "a read or a save must never add a pin of its own: %v", got)
	})

	It("pins Studio and Talk once at the upgrade seed", func() {
		ran, err := svc.Store.SeedExisting(ctx, []string{"u1"}, SeedIDs)
		Expect(err).ToNot(HaveOccurred())
		Expect(ran).To(BeTrue())
		got, err := svc.List(ctx, "u1", vis("studio", "talk"))
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal([]string{"studio", "talk"}), "an account that existed at the upgrade keeps Studio and Talk")
		_, err = svc.Put(ctx, "u1", []string{"talk"}, vis("studio", "talk"))
		Expect(err).ToNot(HaveOccurred())
		ran, _ = svc.Store.SeedExisting(ctx, []string{"u1"}, SeedIDs)
		Expect(ran).To(BeFalse(), "the seed ran a second time")
		got, _ = svc.List(ctx, "u1", vis("studio", "talk"))
		Expect(got).To(Equal([]string{"talk"}), "an unpin did not stick")
	})

	It("keeps pins the caller cannot see on Put", func() {
		_ = svc.Store.Replace(ctx, "u1", []string{"a", "hidden", "b", "c"})
		got, err := svc.Put(ctx, "u1", []string{"c", "a", "b"}, vis("a", "b", "c"))
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal([]string{"c", "a", "b"}))
		stored, _ := svc.Store.List(ctx, "u1")
		Expect(idsOf(stored)).To(Equal([]string{"c", "a", "b", "hidden"}), "the hidden pin was lost or the order is wrong")
	})

	It("drops unknown and duplicate ids on Put", func() {
		got, err := svc.Put(ctx, "u1", []string{"a", "nope", "a", "b"}, vis("a", "b"))
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal([]string{"a", "b"}))
	})

	It("rejects a list over the hard limit", func() {
		long := make([]string, HardLimit+1)
		for i := range long {
			long[i] = "a"
		}
		_, err := svc.Put(context.Background(), "u1", long, vis("a"))
		Expect(err).To(MatchError(ErrTooMany))
	})

	It("never reveals a hidden id on List", func() {
		_ = svc.Store.Replace(ctx, "u1", []string{"secret", "studio"})
		got, _ := svc.List(ctx, "u1", vis("studio"))
		Expect(got).To(Equal([]string{"studio"}), "a hidden id leaked")
	})

	// A corrupt pins file must stop startup with an error. Falling back to memory
	// would lose every unpin at the next restart.
	It("makes the constructor fail on a corrupt pins file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "addon-pins.json")
		Expect(os.WriteFile(path, []byte("{not json"), 0o600)).To(Succeed())
		s, err := NewFileStore(path)
		Expect(err).To(HaveOccurred())
		Expect(s).To(BeNil())
	})

	It("accepts exactly the hard limit", func() {
		at := make([]string, HardLimit)
		over := make([]string, HardLimit+1)
		for i := range over {
			over[i] = "a"
			if i < HardLimit {
				at[i] = "a"
			}
		}
		_, err := svc.Put(context.Background(), "u1", at, vis("a"))
		Expect(err).ToNot(HaveOccurred(), "%d ids must be accepted", HardLimit)
		_, err = svc.Put(context.Background(), "u1", over, vis("a"))
		Expect(err).To(MatchError(ErrTooMany), "%d ids must be refused with ErrTooMany", HardLimit+1)
	})
})
